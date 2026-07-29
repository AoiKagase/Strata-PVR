package wui

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLiveSubtitleManagerSharesOneSource(t *testing.T) {
	manager := newLiveSubtitleManager()
	sourceReader, sourceWriter := io.Pipe()
	var starts atomic.Int32
	start := func(context.Context) (liveSubtitleSource, error) {
		starts.Add(1)
		return liveSubtitleSource{
			output: sourceReader,
			wait:   func() error { return nil },
			close:  sourceReader.Close,
		}, nil
	}

	first, err := manager.subscribe(context.Background(), "channel-1", start)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := manager.subscribe(context.Background(), "channel-1", start)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if got := starts.Load(); got != 1 {
		t.Fatalf("subtitle source starts = %d, want 1", got)
	}

	go func() {
		_, _ = io.WriteString(sourceWriter, "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nshared\n\n")
		_ = sourceWriter.Close()
	}()

	want := "00:00:01.000 --> 00:00:02.000\nshared\n\n"
	for index, reader := range []io.Reader{first, second} {
		done := make(chan string, 1)
		go func(r io.Reader) {
			data, _ := io.ReadAll(r)
			done <- string(data)
		}(reader)
		select {
		case got := <-done:
			if got != want {
				t.Fatalf("subscriber %d = %q, want %q", index, got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("subscriber %d timed out", index)
		}
	}
}

func TestCopySharedLiveWebVTTAddsHeader(t *testing.T) {
	response := httptest.NewRecorder()
	if _, err := copySharedLiveWebVTT(response, strings.NewReader("cue\n")); err != nil {
		t.Fatal(err)
	}
	if got, want := response.Body.String(), "WEBVTT\n\ncue\n"; got != want {
		t.Fatalf("response = %q, want %q", got, want)
	}
	if !response.Flushed {
		t.Fatal("WebVTT header was not flushed")
	}
}

type countingFlushRecorder struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
}

func (r *countingFlushRecorder) Flush() {
	r.ResponseRecorder.Flush()
	r.flushed <- struct{}{}
}

func TestCopySharedLiveWebVTTFlushesEachCueBeforeSourceCloses(t *testing.T) {
	sourceReader, sourceWriter := io.Pipe()
	response := &countingFlushRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		flushed:          make(chan struct{}, 2),
	}
	done := make(chan error, 1)
	go func() {
		_, err := copySharedLiveWebVTT(response, sourceReader)
		done <- err
	}()

	select {
	case <-response.flushed:
	case <-time.After(time.Second):
		t.Fatal("WebVTT header was not flushed")
	}
	if _, err := io.WriteString(sourceWriter, "00:00:01.000 --> 00:00:02.000\nlive\n\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-response.flushed:
		if got := response.Body.String(); !strings.Contains(got, "\nlive\n") {
			t.Fatalf("flushed response = %q, want live cue", got)
		}
	case <-time.After(time.Second):
		t.Fatal("live subtitle cue was buffered until the source closed")
	}

	_ = sourceWriter.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("copy error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("copy did not stop after source closed")
	}
}

func TestServerCachesARIBCaptionDecoderDetection(t *testing.T) {
	old := runFFmpegDecoders
	defer func() { runFFmpegDecoders = old }()
	var calls atomic.Int32
	runFFmpegDecoders = func() ([]byte, error) {
		calls.Add(1)
		return []byte(" S..... libaribcaption ARIB caption decoder"), nil
	}

	server := &server{}
	for range 2 {
		decoder, err := server.aribCaptionDecoder()
		if err != nil {
			t.Fatal(err)
		}
		if decoder != "libaribcaption" {
			t.Fatalf("decoder = %q", decoder)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("ffmpeg decoder detections = %d, want 1", got)
	}
}
