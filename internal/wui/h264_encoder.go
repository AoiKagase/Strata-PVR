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
	done    bool
	encoder string
	err     error
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
	h264EncoderDetection.Lock()
	defer h264EncoderDetection.Unlock()
	if h264EncoderDetection.done {
		return h264EncoderDetection.encoder, h264EncoderDetection.err
	}
	h264EncoderDetection.done = true
	var failures []error
	for _, encoder := range []string{"libx264", "libopenh264"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := runH264EncoderProbe(ctx, encoder)
		cancel()
		if err == nil {
			h264EncoderDetection.encoder = encoder
			return encoder, nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", encoder, err))
	}
	h264EncoderDetection.err = fmt.Errorf("no usable H.264 encoder (tried libx264, libopenh264): %w", errors.Join(failures...))
	return "", h264EncoderDetection.err
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
