package wui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHLSFFmpegArgsUseIPhoneCompatibleCodecs(t *testing.T) {
	dir := t.TempDir()
	args := hlsFFmpegArgs("recording.m2ts", dir, hlsPresets["540p"], 12, 30, "secondary", "libopenh264")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-re", "-ss 12", "-t 30", "-map 0:a:1?", "-c:v libopenh264", "-profile:v constrained_baseline", "-pix_fmt yuv420p",
		"-c:a aac", "-ac 2", "-ar 48000", "-hls_time 4", "-hls_playlist_type event",
		filepath.Join(dir, "segment%05d.ts"),
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args do not contain %q: %s", want, joined)
		}
	}
}

func TestHLSFFmpegArgsCanReadLiveInputFromPipe(t *testing.T) {
	args := hlsFFmpegArgs("pipe:0", t.TempDir(), hlsPresets["540p"], 0, 0, "", "libx264")
	joined := strings.Join(args, " ")
	for _, want := range []string{"-re", "-f mpegts -i pipe:0", "-hls_playlist_type event", "-hls_time 4"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("live HLS args missing %q: %s", want, joined)
		}
	}
}

func TestHLSSessionManagerClosesUnusedLiveInput(t *testing.T) {
	m := newHLSSessionManager(Paths{})
	key := channelHLSKey("abc")
	id := hlsSessionID(key, "540p", 0, 0, "")
	existing := &hlsSession{id: id, timer: time.NewTimer(time.Hour)}
	defer existing.timer.Stop()
	m.sessions[id] = existing
	input := &testHLSReadCloser{Reader: strings.NewReader("unused")}
	got, err := m.getOrStartStream(key, input, "540p", hlsPresets["540p"], 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != existing {
		t.Fatal("live HLS did not reuse the existing session")
	}
	if !input.closed {
		t.Fatal("unused live input was not closed")
	}
}

type testHLSReadCloser struct {
	*strings.Reader
	closed bool
}

func (r *testHLSReadCloser) Close() error {
	r.closed = true
	return nil
}

func TestHLSSessionStopCancelsAndRemovesSession(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	m := newHLSSessionManager(Paths{})
	id := hlsSessionID("recording.m2ts", "540p", 12, 30, "")
	m.sessions[id] = &hlsSession{id: id, dir: dir, cancel: cancel, done: make(chan struct{})}

	m.stop("recording.m2ts", "540p", 12, 30, "")

	select {
	case <-ctx.Done():
	default:
		t.Fatal("session context was not cancelled")
	}
	if m.sessions[id] != nil {
		t.Fatal("session was not removed")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("session directory still exists: %v", err)
	}
}

func TestAddHLSSessionQueryOnlyChangesSegments(t *testing.T) {
	got := addHLSSessionQuery("#EXTM3U\n#EXTINF:4,\nsegment00001.ts\n#EXT-X-ENDLIST\n", "0123456789abcdef01234567")
	want := "segment00001.ts?session=0123456789abcdef01234567"
	if !strings.Contains(got, want) {
		t.Fatalf("playlist = %q, want it to contain %q", got, want)
	}
	if strings.Contains(got, "#EXTM3U?") {
		t.Fatalf("playlist metadata was modified: %q", got)
	}
}

func TestValidHLSSegmentName(t *testing.T) {
	for _, name := range []string{"segment00000.ts", "segment42.ts"} {
		if !validHLSSegmentName(name) {
			t.Errorf("expected valid: %q", name)
		}
	}
	for _, name := range []string{"../segment1.ts", "segment.ts", "segment1.mp4", "other1.ts"} {
		if validHLSSegmentName(name) {
			t.Errorf("expected invalid: %q", name)
		}
	}
}
