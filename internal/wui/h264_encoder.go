package wui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

var h264EncoderDetection struct {
	sync.Mutex
	done     bool
	encoders []h264EncoderCapability
	err      error
}

type h264EncoderCapability struct {
	Name     string `json:"name"`
	Hardware bool   `json:"hardware"`
}

var h264EncoderCandidates = []h264EncoderCapability{
	{Name: "libx264"},
	{Name: "libopenh264"},
	{Name: "h264_nvenc", Hardware: true},
	{Name: "h264_qsv", Hardware: true},
	{Name: "h264_amf", Hardware: true},
	{Name: "h264_videotoolbox", Hardware: true},
}

var runH264EncoderProbe = func(ctx context.Context, encoder string) error {
	// A raw 64x64 YUV420P frame avoids depending on the lavfi input device.
	frame := make([]byte, 64*64*3/2)
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-v", "error", "-f", "rawvideo", "-pix_fmt", "yuv420p", "-s:v", "64x64", "-r", "1",
		"-i", "pipe:0", "-frames:v", "1", "-c:v", encoder, "-f", "null", "-")
	cmd.Stdin = bytes.NewReader(frame)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() != 0 {
			return fmt.Errorf("%w: %s", err, stderr.String())
		}
		return err
	}
	return nil
}

func detectedH264Encoder() (string, error) {
	encoders, err := availableH264Encoders()
	if err != nil {
		return "", err
	}
	for _, encoder := range encoders {
		if !encoder.Hardware {
			return encoder.Name, nil
		}
	}
	return "", errors.New("no usable software H.264 encoder found")
}

func availableH264Encoders() ([]h264EncoderCapability, error) {
	h264EncoderDetection.Lock()
	defer h264EncoderDetection.Unlock()
	if h264EncoderDetection.done {
		return append([]h264EncoderCapability(nil), h264EncoderDetection.encoders...), h264EncoderDetection.err
	}
	h264EncoderDetection.done = true
	var failures []error
	for _, encoder := range h264EncoderCandidates {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := runH264EncoderProbe(ctx, encoder.Name)
		cancel()
		if err == nil {
			h264EncoderDetection.encoders = append(h264EncoderDetection.encoders, encoder)
			continue
		}
		failures = append(failures, fmt.Errorf("%s: %w", encoder.Name, err))
	}
	if len(h264EncoderDetection.encoders) == 0 {
		h264EncoderDetection.err = fmt.Errorf("no usable H.264 encoder: %w", errors.Join(failures...))
	}
	return append([]h264EncoderCapability(nil), h264EncoderDetection.encoders...), h264EncoderDetection.err
}

func appendH264CompatibilityArgs(args []string, encoder string) []string {
	switch encoder {
	case "libx264":
		return append(args, "-profile:v", "main", "-preset", "veryfast", "-pix_fmt", "yuv420p")
	case "libopenh264":
		return append(args, "-profile:v", "constrained_baseline", "-pix_fmt", "yuv420p")
	default:
		return args
	}
}
