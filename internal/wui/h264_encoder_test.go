package wui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"

	"strata-pvr/internal/config"
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

func TestAvailableH264EncodersIncludesUsableHardware(t *testing.T) {
	withH264Probe(t, func(_ context.Context, encoder string) error {
		if encoder == "libx264" || encoder == "h264_nvenc" {
			return nil
		}
		return errors.New("not available")
	})
	encoders, err := availableH264Encoders()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := encoders, []h264EncoderCapability{{Name: "libx264"}, {Name: "h264_nvenc", Hardware: true}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("encoders=%#v, want %#v", got, want)
	}
}

func TestMP4VideoEncoderUsesConfiguredHardwareEncoder(t *testing.T) {
	withH264Probe(t, func(_ context.Context, encoder string) error {
		if encoder == "libx264" || encoder == "h264_nvenc" {
			return nil
		}
		return errors.New("not available")
	})
	s := &server{cfg: &config.Config{MP4VideoEncoder: "h264_nvenc"}}
	encoder, err := s.mp4VideoEncoder()
	if err != nil || encoder != "h264_nvenc" {
		t.Fatalf("encoder=%q err=%v", encoder, err)
	}
	args := watchFFmpegArgs(httptest.NewRequest(http.MethodGet, "/watch.mp4", nil), "mp4", true, encoder)
	if !slices.Contains(args, "h264_nvenc") {
		t.Fatalf("ffmpeg args do not select configured hardware encoder: %v", args)
	}
}

func TestWatchFFmpegArgsUsesFrequentAMFKeyframes(t *testing.T) {
	args := watchFFmpegArgs(httptest.NewRequest(http.MethodGet, "/watch.mp4", nil), "mp4", true, "h264_amf")
	for _, pair := range [][2]string{{"-bf", "0"}, {"-g", "60"}, {"-forced_idr", "1"}} {
		found := false
		for i := range args {
			if args[i] == pair[0] && i+1 < len(args) && args[i+1] == pair[1] {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("AMF args missing %q %q: %v", pair[0], pair[1], args)
		}
	}
	for i := range args {
		if args[i] == "-af" && i+1 < len(args) && args[i+1] == "aresample=async=1:first_pts=0" {
			break
		}
		if i == len(args)-1 {
			t.Fatalf("AMF args do not normalize audio timestamps: %v", args)
		}
	}
	for i := range args {
		if args[i] == "-frag_interleave" && i+1 < len(args) && args[i+1] == "1" {
			break
		}
		if i == len(args)-1 {
			t.Fatalf("AMF args do not interleave fragmented MP4 samples: %v", args)
		}
	}
	for i := range args {
		if args[i] == "-flush_packets" && i+1 < len(args) && args[i+1] == "1" {
			return
		}
	}
	t.Fatalf("AMF args do not flush MP4 packets: %v", args)
}

func TestAPIEncodersReturnsOnlyUsableEncoders(t *testing.T) {
	withH264Probe(t, func(_ context.Context, encoder string) error {
		if encoder == "libx264" || encoder == "h264_nvenc" {
			return nil
		}
		return errors.New("not available")
	})
	response := httptest.NewRecorder()
	newHandler(Paths{}, &config.Config{}, false).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/encoders", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var encoders []h264EncoderCapability
	if err := json.Unmarshal(response.Body.Bytes(), &encoders); err != nil {
		t.Fatal(err)
	}
	if got, want := encoders, []h264EncoderCapability{{Name: "libx264"}, {Name: "h264_nvenc", Hardware: true}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("encoders=%#v, want %#v", got, want)
	}
}

func withH264Probe(t *testing.T, probe func(context.Context, string) error) {
	t.Helper()
	oldProbe := runH264EncoderProbe
	h264EncoderDetection.Lock()
	oldDone, oldEncoders, oldErr := h264EncoderDetection.done, h264EncoderDetection.encoders, h264EncoderDetection.err
	h264EncoderDetection.Unlock()
	runH264EncoderProbe = probe
	h264EncoderDetection.Lock()
	h264EncoderDetection.done = false
	h264EncoderDetection.encoders = nil
	h264EncoderDetection.err = nil
	h264EncoderDetection.Unlock()
	t.Cleanup(func() {
		runH264EncoderProbe = oldProbe
		h264EncoderDetection.Lock()
		h264EncoderDetection.done, h264EncoderDetection.encoders, h264EncoderDetection.err = oldDone, oldEncoders, oldErr
		h264EncoderDetection.Unlock()
	})
}
