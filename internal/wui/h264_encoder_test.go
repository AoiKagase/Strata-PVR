package wui

import (
	"context"
	"errors"
	"testing"
)

func TestDetectH264EncoderPrefersX264(t *testing.T) {
	withH264Probe(t, func(_ context.Context, encoder string) error {
		if encoder == "libx264" {
			return nil
		}
		return errors.New("unexpected fallback")
	})
	encoder, err := detectedH264Encoder()
	if err != nil || encoder != "libx264" {
		t.Fatalf("encoder=%q err=%v", encoder, err)
	}
}

func TestDetectH264EncoderFallsBackToOpenH264(t *testing.T) {
	withH264Probe(t, func(_ context.Context, encoder string) error {
		if encoder == "libopenh264" {
			return nil
		}
		return errors.New("not available")
	})
	encoder, err := detectedH264Encoder()
	if err != nil || encoder != "libopenh264" {
		t.Fatalf("encoder=%q err=%v", encoder, err)
	}
}

func TestDetectH264EncoderReportsFailure(t *testing.T) {
	withH264Probe(t, func(context.Context, string) error { return errors.New("not available") })
	if encoder, err := detectedH264Encoder(); err == nil || encoder != "" {
		t.Fatalf("encoder=%q err=%v", encoder, err)
	}
}

func withH264Probe(t *testing.T, probe func(context.Context, string) error) {
	t.Helper()
	oldProbe := runH264EncoderProbe
	h264EncoderDetection.Lock()
	oldDone, oldEncoder, oldErr := h264EncoderDetection.done, h264EncoderDetection.encoder, h264EncoderDetection.err
	h264EncoderDetection.Unlock()
	runH264EncoderProbe = probe
	h264EncoderDetection.Lock()
	h264EncoderDetection.done = false
	h264EncoderDetection.encoder = ""
	h264EncoderDetection.err = nil
	h264EncoderDetection.Unlock()
	t.Cleanup(func() {
		runH264EncoderProbe = oldProbe
		h264EncoderDetection.Lock()
		h264EncoderDetection.done, h264EncoderDetection.encoder, h264EncoderDetection.err = oldDone, oldEncoder, oldErr
		h264EncoderDetection.Unlock()
	})
}
