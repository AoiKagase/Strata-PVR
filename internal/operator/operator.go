package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"chinachu-go/internal/chinachu"
	"chinachu-go/internal/config"
	"chinachu-go/internal/mirakurun"
	"chinachu-go/internal/storage"
)

const recordStartMargin = 15 * time.Second

type StreamSource interface {
	ProgramStream(context.Context, int64, bool) (io.ReadCloser, error)
}

type Paths struct {
	Config    string
	Reserves  string
	Recording string
	Recorded  string
}

type Result struct {
	Started   int
	Completed int
	Failed    int
}

func Run(ctx context.Context, paths Paths, interval time.Duration) error {
	cfg, err := config.Load(paths.Config)
	if err != nil {
		return err
	}
	client, err := mirakurun.New(cfg.EffectiveMirakurunPath())
	if err != nil {
		return err
	}
	client.Priority = cfg.RecordingPriority
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := RunOnce(ctx, paths, cfg, client, time.Now()); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func RunOnce(ctx context.Context, paths Paths, cfg *config.Config, source StreamSource, now time.Time) (Result, error) {
	var reserves []chinachu.Program
	if err := storage.ReadJSON(paths.Reserves, &reserves, "[]"); err != nil {
		return Result{}, err
	}
	var recording []chinachu.Program
	if err := storage.ReadJSON(paths.Recording, &recording, "[]"); err != nil {
		return Result{}, err
	}
	var recorded []chinachu.Program
	if err := storage.ReadJSON(paths.Recorded, &recorded, "[]"); err != nil {
		return Result{}, err
	}

	result := Result{}
	for _, reserve := range reserves {
		if !shouldStart(reserve, recording, now) {
			continue
		}
		recording = append(recording, reserve)
		if err := storage.WriteJSONAtomic(paths.Recording, recording, false); err != nil {
			return result, err
		}
		result.Started++

		completed, err := recordProgram(ctx, paths.Recording, cfg, source, reserve)
		recording = removeProgram(recording, reserve.ID)
		if writeErr := storage.WriteJSONAtomic(paths.Recording, recording, false); writeErr != nil && err == nil {
			err = writeErr
		}
		if err != nil {
			result.Failed++
			return result, err
		}

		recorded = append(recorded, completed)
		reserves = removeProgram(reserves, reserve.ID)
		if err := storage.WriteJSONAtomic(paths.Recorded, recorded, false); err != nil {
			return result, err
		}
		if err := storage.WriteJSONAtomic(paths.Reserves, reserves, false); err != nil {
			return result, err
		}
		if err := runRecordedCommand(ctx, cfg.RecordedCommand, completed); err != nil {
			result.Failed++
			return result, err
		}
		result.Completed++
	}
	return result, nil
}

func shouldStart(program chinachu.Program, recording []chinachu.Program, now time.Time) bool {
	if program.IsSkip || program.IsConflict || program.End <= now.UnixMilli() {
		return false
	}
	if containsProgram(recording, program.ID) {
		return false
	}
	startAt := time.UnixMilli(program.Start).Add(-recordStartMargin)
	return !now.Before(startAt)
}

func recordProgram(ctx context.Context, recordingPath string, cfg *config.Config, source StreamSource, program chinachu.Program) (chinachu.Program, error) {
	streamID, err := strconv.ParseInt(program.ID, 36, 64)
	if err != nil {
		return program, fmt.Errorf("parse program id %q: %w", program.ID, err)
	}
	recordCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := source.ProgramStream(recordCtx, streamID, true)
	if err != nil {
		return program, err
	}
	defer stream.Close()
	aborted, stopAbortMonitor := watchAbortFlag(recordCtx, recordingPath, program.ID, cancel, stream)
	defer stopAbortMonitor()

	format := cfg.RecordedFormat
	if program.RecordedFormat != "" {
		format = program.RecordedFormat
	}
	relativeName := chinachu.FormatRecordedName(program, format)
	finalPath := filepath.Join(cfg.RecordedDir, filepath.FromSlash(relativeName))
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return program, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(finalPath), "."+filepath.Base(finalPath)+".recording-*")
	if err != nil {
		return program, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, stream); err != nil && !aborted.Load() {
		tmp.Close()
		return program, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return program, err
	}
	if err := tmp.Close(); err != nil {
		return program, err
	}
	if err := os.Rename(tmpName, finalPath); err != nil {
		return program, err
	}
	program.Recorded = filepath.ToSlash(finalPath)
	return program, nil
}

func watchAbortFlag(ctx context.Context, recordingPath, programID string, cancel context.CancelFunc, stream io.Closer) (*atomic.Bool, func()) {
	var aborted atomic.Bool
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				var recording []chinachu.Program
				if err := storage.ReadJSON(recordingPath, &recording, "[]"); err != nil {
					continue
				}
				for _, program := range recording {
					if program.ID == programID && program.Abort {
						aborted.Store(true)
						cancel()
						_ = stream.Close()
						return
					}
				}
			}
		}
	}()
	return &aborted, func() {
		close(done)
		<-stopped
	}
}

func runRecordedCommand(ctx context.Context, command string, program chinachu.Program) error {
	if command == "" {
		return nil
	}
	payload, err := json.Marshal(program)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, command, filepath.FromSlash(program.Recorded), string(payload))
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func removeProgram(programs []chinachu.Program, id string) []chinachu.Program {
	out := programs[:0]
	for _, program := range programs {
		if program.ID != id {
			out = append(out, program)
		}
	}
	return out
}

func containsProgram(programs []chinachu.Program, id string) bool {
	for _, program := range programs {
		if program.ID == id {
			return true
		}
	}
	return false
}

func IsContextCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
