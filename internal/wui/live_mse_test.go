package wui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProbeLiveMSEInputDetectsCaptionAndReplaysProbeBytes(t *testing.T) {
	pat := liveMSEPATSection(map[uint16]uint16{1: 0x100})
	pmt := liveMSEPMTSection(1, []liveMSETestStream{
		{streamType: 0x1b, pid: 0x101},
		{streamType: 0x0f, pid: 0x102},
		{streamType: 0x06, pid: 0x120, descriptors: []byte{0xfd, 0x02, 0x00, 0x08}},
	})
	stream := append([]byte{0x00, 0x11, 0x22, 0x33, 0x44}, liveMSEPSIPacket(0, pat)...)
	stream = append(stream, liveMSEPSIPacket(0x100, pmt)...)

	input, hasCaption, err := probeLiveMSEInput(io.NopCloser(bytes.NewReader(stream)))
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if !hasCaption {
		t.Fatal("ARIB caption stream was not detected")
	}
	replayed, err := io.ReadAll(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(replayed, stream) {
		t.Fatalf("probe consumed input bytes: got=%d want=%d", len(replayed), len(stream))
	}
}

func TestMPEGTSSubtitleProbeWaitsForAllPATPrograms(t *testing.T) {
	probe := newMPEGTSSubtitleProbe()
	probe.feed(liveMSEPSIPacket(0, liveMSEPATSection(map[uint16]uint16{
		1: 0x100,
		2: 0x200,
	})))
	probe.feed(liveMSEPSIPacket(0x100, liveMSEPMTSection(1, []liveMSETestStream{
		{streamType: 0x1b, pid: 0x101},
	})))
	if probe.complete {
		t.Fatal("probe completed before every PMT was inspected")
	}
	probe.feed(liveMSEPSIPacket(0x200, liveMSEPMTSection(2, []liveMSETestStream{
		{streamType: 0x06, pid: 0x220, descriptors: []byte{0x52, 0x01, 0x30}},
	})))
	if !probe.complete || !probe.hasCaption {
		t.Fatalf("caption PMT result: complete=%v hasCaption=%v", probe.complete, probe.hasCaption)
	}
}

func TestMPEGTSSubtitleProbeHandlesChunkedInputWithoutCaption(t *testing.T) {
	stream := append(liveMSEPSIPacket(0, liveMSEPATSection(map[uint16]uint16{1: 0x100})),
		liveMSEPSIPacket(0x100, liveMSEPMTSection(1, []liveMSETestStream{
			{streamType: 0x1b, pid: 0x101},
			{streamType: 0x0f, pid: 0x102},
		}))...)
	probe := newMPEGTSSubtitleProbe()
	for len(stream) > 0 {
		size := 37
		if len(stream) < size {
			size = len(stream)
		}
		probe.feed(stream[:size])
		stream = stream[size:]
	}
	if !probe.complete || probe.hasCaption {
		t.Fatalf("probe result: complete=%v hasCaption=%v", probe.complete, probe.hasCaption)
	}
}

func TestPSIAssemblerHandlesSectionSplitAcrossPayloads(t *testing.T) {
	section := liveMSEPMTSection(1, []liveMSETestStream{
		{streamType: 0x06, pid: 0x120, descriptors: []byte{0xfd, 0x02, 0x00, 0x08}},
	})
	assembler := &psiAssembler{}
	if got := assembler.feed(append([]byte{0x00}, section[:9]...), true); len(got) != 0 {
		t.Fatalf("first payload unexpectedly produced %d section(s)", len(got))
	}
	got := assembler.feed(section[9:], false)
	if len(got) != 1 || !bytes.Equal(got[0], section) {
		t.Fatalf("split section was not reassembled: %#v", got)
	}
}

func TestLiveMSEFFmpegArgsUseOneProcessForM2TSAndWebVTT(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/api/channel/abc/watch.m2ts?mode=mse&s=1280x720&audio=secondary", nil)
	if err != nil {
		t.Fatal(err)
	}
	args := liveMSEFFmpegArgs(req, "libx264", "libaribcaption", "tcp://127.0.0.1:21000")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-c:s libaribcaption -sub_type ass -fflags",
		"-map 0:v:0 -map 0:a:1? -sn -dn",
		"-c:v libx264",
		"-s 1280x720",
		"-y -f mpegts pipe:1 -map 0:s:0 -vn -an -c:s webvtt",
		"-f webvtt tcp://127.0.0.1:21000",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("FFmpeg args missing %q: %s", want, joined)
		}
	}
	if strings.Count(joined, "pipe:1") != 1 {
		t.Fatalf("unexpected media outputs: %s", joined)
	}
}

func TestLiveMSEFFmpegArgsOmitSubtitleOutputWhenUnavailable(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/api/channel/abc/watch.m2ts?mode=mse", nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(liveMSEFFmpegArgs(req, "libx264", "", ""), " ")
	if strings.Contains(joined, "webvtt") || strings.Contains(joined, "tcp://") {
		t.Fatalf("subtitle output should be omitted: %s", joined)
	}
	if strings.Count(joined, "pipe:1") != 1 {
		t.Fatalf("media output missing: %s", joined)
	}
}

func TestLiveVTTHubKeepsCueBoundariesWhenSubscriberFallsBehind(t *testing.T) {
	hub := newLiveVTTHub()
	reader := hub.subscribe(context.Background())
	for index := 0; index < liveMSESubtitleQueueSize+8; index++ {
		hub.broadcast([]byte(liveMSETestCue(index)))
	}
	hub.finish()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	blocks := strings.Split(strings.TrimSpace(string(body)), "\n\n")
	if len(blocks) != liveMSESubtitleQueueSize {
		t.Fatalf("queued cue count=%d want=%d", len(blocks), liveMSESubtitleQueueSize)
	}
	for _, block := range blocks {
		if !strings.Contains(block, "-->") || !strings.Contains(block, "cue-") {
			t.Fatalf("partial cue was queued: %q", block)
		}
	}
	if strings.Contains(string(body), "cue-0\n") || !strings.Contains(string(body), "cue-39\n") {
		t.Fatalf("queue did not retain the newest complete cues: %q", body)
	}
}

func TestLiveVTTHubConsumesWebVTTAcrossArbitraryReadChunks(t *testing.T) {
	hub := newLiveVTTHub()
	hub.consume(&liveMSEChunkReader{
		chunks: []string{
			"WEB", "VTT\r", "\n\r\n00:00:01.000 --> 00:00:02.000\r\nfirst",
			"\r\n\r", "\n00:00:03.000 --> 00:00:04.000\nsecond\n\n",
		},
	})
	reader := hub.subscribe(context.Background())
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(body), "-->"); got != 2 {
		t.Fatalf("cue count=%d body=%q", got, body)
	}
	for _, want := range []string{"first\n\n", "second\n\n"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("chunked WebVTT missing %q: %q", want, body)
		}
	}
}

func TestLiveVTTHubReplayIsBounded(t *testing.T) {
	hub := newLiveVTTHub()
	for index := 0; index < liveMSESubtitleCueLimit; index++ {
		hub.broadcast([]byte(liveMSETestCue(index)))
	}
	reader := hub.subscribe(context.Background())
	hub.finish()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(body), "-->"); got != liveMSESubtitleQueueSize {
		t.Fatalf("replayed cue count=%d want=%d", got, liveMSESubtitleQueueSize)
	}
	if !strings.Contains(string(body), "cue-127") {
		t.Fatalf("newest cue missing from replay: %q", body)
	}
}

func TestLiveMSESessionManagerStopDuringStartCannotLeakSession(t *testing.T) {
	manager := newLiveMSESessionManager()
	entered := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := manager.start(context.Background(), "playback_session_123", "key", func(_ context.Context, cancel context.CancelFunc) (*liveMSESession, error) {
			close(entered)
			<-release
			done := make(chan struct{})
			close(done)
			return &liveMSESession{cancel: cancel, done: done}, nil
		})
		result <- err
	}()
	<-entered
	manager.stop("playback_session_123")
	close(release)
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("start error=%v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("session start did not finish after stop")
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.sessions) != 0 || len(manager.starting) != 0 {
		t.Fatalf("stopped session leaked: sessions=%d starting=%d", len(manager.sessions), len(manager.starting))
	}
}

func TestLiveMSESessionManagerSharesStartingSessionWithSubtitleWaiter(t *testing.T) {
	manager := newLiveMSESessionManager()
	entered := make(chan struct{})
	release := make(chan struct{})
	started := make(chan *liveMSESession, 1)
	go func() {
		session, _ := manager.start(context.Background(), "playback_session_456", "key", func(ctx context.Context, cancel context.CancelFunc) (*liveMSESession, error) {
			close(entered)
			<-release
			return &liveMSESession{ctx: ctx, cancel: cancel, done: make(chan struct{})}, nil
		})
		started <- session
	}()
	<-entered
	waited := make(chan *liveMSESession, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		session, _ := manager.wait(ctx, "playback_session_456")
		waited <- session
	}()
	close(release)
	session := <-started
	if session == nil || <-waited != session {
		t.Fatal("subtitle waiter did not receive the video session")
	}
	session.finish()
	manager.stop("playback_session_456")
}

func TestHandleLiveMSESubtitlesReportsAbsentAndUnavailableCaptions(t *testing.T) {
	for _, test := range []struct {
		name        string
		subtitleErr error
		status      int
	}{
		{name: "absent", status: http.StatusNoContent},
		{name: "decoder unavailable", subtitleErr: errors.New("libaribcaption unavailable"), status: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := newLiveMSESessionManager()
			manager.sessions["playback_session_789"] = &liveMSESession{
				cancel:      func() {},
				subtitleErr: test.subtitleErr,
				done:        make(chan struct{}),
			}
			s := &server{liveMSE: manager}
			req := httptest.NewRequest(http.MethodGet, "/api/channel/abc/subtitles.vtt?mode=mse&session=playback_session_789", nil)
			res := httptest.NewRecorder()
			s.handleLiveMSESubtitles(res, req)
			if res.Code != test.status {
				t.Fatalf("status=%d body=%q want=%d", res.Code, res.Body.String(), test.status)
			}
		})
	}
}

type liveMSETestStream struct {
	streamType  byte
	pid         uint16
	descriptors []byte
}

type liveMSEChunkReader struct {
	chunks []string
}

func (r *liveMSEChunkReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := r.chunks[0]
	r.chunks = r.chunks[1:]
	return copy(p, chunk), nil
}

func liveMSEPATSection(programs map[uint16]uint16) []byte {
	length := 5 + 4*len(programs) + 4
	section := []byte{0x00, 0xb0 | byte(length>>8), byte(length), 0x00, 0x01, 0xc1, 0x00, 0x00}
	for program := uint16(1); program <= uint16(len(programs)); program++ {
		pid, ok := programs[program]
		if !ok {
			continue
		}
		section = append(section, byte(program>>8), byte(program), 0xe0|byte(pid>>8), byte(pid))
	}
	return append(section, 0x00, 0x00, 0x00, 0x00)
}

func liveMSEPMTSection(program uint16, streams []liveMSETestStream) []byte {
	streamBytes := make([]byte, 0)
	for _, stream := range streams {
		infoLength := len(stream.descriptors)
		streamBytes = append(streamBytes,
			stream.streamType,
			0xe0|byte(stream.pid>>8), byte(stream.pid),
			0xf0|byte(infoLength>>8), byte(infoLength),
		)
		streamBytes = append(streamBytes, stream.descriptors...)
	}
	length := 9 + len(streamBytes) + 4
	section := []byte{
		0x02, 0xb0 | byte(length>>8), byte(length),
		byte(program >> 8), byte(program), 0xc1, 0x00, 0x00,
		0xe1, 0x01,
		0xf0, 0x00,
	}
	section = append(section, streamBytes...)
	return append(section, 0x00, 0x00, 0x00, 0x00)
}

func liveMSEPSIPacket(pid uint16, section []byte) []byte {
	packet := bytes.Repeat([]byte{0xff}, 188)
	packet[0] = 0x47
	packet[1] = 0x40 | byte(pid>>8)
	packet[2] = byte(pid)
	packet[3] = 0x10
	packet[4] = 0x00
	copy(packet[5:], section)
	return packet
}

func liveMSETestCue(index int) string {
	return "00:00:01.000 --> 00:00:02.000\ncue-" + strconv.Itoa(index) + "\n\n"
}
