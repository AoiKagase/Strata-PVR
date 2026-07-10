package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"strata-pvr/internal/config"
	"strata-pvr/internal/database"
	"strata-pvr/internal/legacy"
	"strata-pvr/internal/logging"
	"strata-pvr/internal/mirakurun"
	"strata-pvr/internal/programstore"
	"strata-pvr/internal/reservationstore"
	"strata-pvr/internal/storage"
	"strata-pvr/internal/system"
)

const recordStartMargin = 15 * time.Second

var getDiskUsage = system.GetDiskUsage

type StreamSource interface {
	ProgramStream(context.Context, int64, bool) (io.ReadCloser, error)
}

type prioritySetter interface {
	SetPriority(int)
}

type Paths struct {
	Config    string
	Database  string
	Reserves  string
	Recording string
	Recorded  string
	PID       string
	Log       string
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
	if err := writePIDFile(paths.PID); err != nil {
		return err
	}
	defer removePIDFile(paths.PID)
	if err := initializeRuntimeState(paths, cfg); err != nil {
		return err
	}
	if paths.Database != "" {
		db, err := database.Open(ctx, paths.Database)
		if err != nil {
			return err
		}
		defer db.Close()
		ctx = database.WithHandle(ctx, db)
	}

	client, err := mirakurun.New(cfg.EffectiveMirakurunPath())
	if err != nil {
		return err
	}
	client.UserAgent = mirakurun.StrataUserAgent("operator")
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

func initializeRuntimeState(paths Paths, cfg *config.Config) error {
	if err := programstore.Write(context.Background(), paths.Database, paths.Recording, programstore.Recording, []legacy.Program{}); err != nil {
		return err
	}
	recordedDir := cfg.RecordedDir
	if recordedDir == "" {
		recordedDir = "./recorded/"
	}
	if _, err := os.Stat(recordedDir); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := logging.AppendLine(paths.Log, "MKDIR: %s", recordedDir); err != nil {
		return err
	}
	return os.MkdirAll(recordedDir, 0o755)
}

func writePIDFile(path string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
}

func removePIDFile(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}

func RunOnce(ctx context.Context, paths Paths, cfg *config.Config, source StreamSource, now time.Time) (Result, error) {
	reserves, err := reservationstore.Read(ctx, paths.Database, paths.Reserves)
	if err != nil {
		return Result{}, err
	}
	recording, err := programstore.Read(ctx, paths.Database, paths.Recording, programstore.Recording)
	if err != nil {
		return Result{}, err
	}
	recorded, err := programstore.Read(ctx, paths.Database, paths.Recorded, programstore.Recorded)
	if err != nil {
		return Result{}, err
	}
	recorded, err = handleLowStorage(ctx, paths, cfg, recording, recorded)
	if err != nil {
		return Result{}, err
	}

	result := Result{}
	for _, reserve := range reserves {
		if !shouldStart(reserve, recording, now) {
			continue
		}
		if err := logging.AppendLine(paths.Log, "PREPARE: %s", operatorProgramLogLine(reserve)); err != nil {
			return result, err
		}
		recording = append(recording, reserve)
		if err := programstore.Upsert(ctx, paths.Database, paths.Recording, programstore.Recording, reserve); err != nil {
			return result, err
		}
		if err := logging.AppendLine(paths.Log, "WRITE: %s", paths.Recording); err != nil {
			return result, err
		}
		if err := logging.AppendLine(paths.Log, "START: %s [%s] %s", reserve.ID, reserve.Channel.Name, reserve.Title); err != nil {
			return result, err
		}
		result.Started++

		completed, err := recordProgramWithLog(ctx, paths.Recording, paths.Log, cfg, source, reserve)
		recording = removeProgram(recording, reserve.ID)
		if err != nil {
			if writeErr := programstore.Remove(ctx, paths.Database, paths.Recording, programstore.Recording, reserve.ID); writeErr != nil {
				err = errors.Join(err, writeErr)
			}
			result.Failed++
			return result, err
		}

		recorded = mergeRecordedProgram(recorded, completed)
		if paths.Database != "" {
			if err := programstore.Complete(ctx, paths.Database, completed); err != nil {
				return result, err
			}
		} else {
			if err := programstore.Remove(ctx, "", paths.Recording, programstore.Recording, reserve.ID); err != nil {
				return result, err
			}
			if err := programstore.Write(ctx, "", paths.Recorded, programstore.Recorded, recorded); err != nil {
				return result, err
			}
		}
		if err := logging.AppendLine(paths.Log, "WRITE: %s", paths.Recording); err != nil {
			return result, err
		}
		if err := logging.AppendLine(paths.Log, "WRITE: %s", paths.Recorded); err != nil {
			return result, err
		}
		if completed.IsManualReserved {
			reserves = removeProgram(reserves, reserve.ID)
			if _, err := reservationstore.Delete(ctx, paths.Database, paths.Reserves, reserve.ID); err != nil {
				return result, err
			}
			if err := logging.AppendLine(paths.Log, "WRITE: %s", paths.Reserves); err != nil {
				return result, err
			}
		}
		if err := logging.AppendLine(paths.Log, "FIN: %s [%s] %s", completed.ID, completed.Channel.Name, completed.Title); err != nil {
			return result, err
		}
		if err := logging.AppendLine(paths.Log, "FIN: %s", operatorProgramLogLine(completed)); err != nil {
			return result, err
		}
		result.Completed++
	}
	return result, nil
}

func shouldStart(program legacy.Program, recording []legacy.Program, now time.Time) bool {
	if program.IsSkip || program.End <= now.UnixMilli() {
		return false
	}
	if containsProgram(recording, program.ID) {
		return false
	}
	startAt := time.UnixMilli(program.Start).Add(-recordStartMargin)
	return !now.Before(startAt)
}

func recordProgram(ctx context.Context, recordingPath string, cfg *config.Config, source StreamSource, program legacy.Program) (legacy.Program, error) {
	return recordProgramWithLog(ctx, recordingPath, "", cfg, source, program)
}

func recordProgramWithLog(ctx context.Context, recordingPath, logPath string, cfg *config.Config, source StreamSource, program legacy.Program) (legacy.Program, error) {
	streamID, err := strconv.ParseInt(program.ID, 36, 64)
	if err != nil {
		return program, fmt.Errorf("parse program id %q: %w", program.ID, err)
	}
	recordCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if setter, ok := source.(prioritySetter); ok {
		setter.SetPriority(programPriority(cfg, program))
	}
	priority := programPriority(cfg, program)
	stream, err := source.ProgramStream(recordCtx, streamID, true)
	if err != nil {
		return program, err
	}
	defer stream.Close()
	stopContextClose := closeStreamOnContext(recordCtx, stream)
	defer stopContextClose()
	aborted, stopAbortMonitor := watchAbortFlag(recordCtx, recordingPath, program.ID, cancel, stream)
	defer stopAbortMonitor()

	format := cfg.RecordedFormat
	if program.RecordedFormat != "" {
		format = program.RecordedFormat
	}
	relativeName := legacy.FormatRecordedName(program, format)
	finalPath := filepath.Join(cfg.RecordedDir, filepath.FromSlash(relativeName))
	if logPath != "" {
		if err := logging.AppendLine(logPath, "RECORD: %s", operatorProgramLogLine(program)); err != nil {
			return program, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return program, err
	}
	if logPath != "" {
		if err := logging.AppendLine(logPath, "STREAM: %s", finalPath); err != nil {
			return program, err
		}
	}
	program.Recorded = filepath.ToSlash(finalPath)
	program.PID = -1
	setProgramRawJSON(&program, "priority", priority)
	setProgramRawJSON(&program, "tuner", map[string]any{
		"name":         "Mirakurun",
		"command":      "*",
		"isScrambling": false,
	})
	setProgramRawJSON(&program, "command", fmt.Sprintf("mirakurun type=%s priority=%d", program.Channel.Type, priority))
	if err := updateRecordingProgram(recordingPath, program); err != nil {
		return program, err
	}
	if logPath != "" {
		if err := logging.AppendLine(logPath, "WRITE: %s", recordingPath); err != nil {
			return program, err
		}
	}
	out, err := os.OpenFile(finalPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return program, err
	}
	if _, err := io.Copy(out, stream); err != nil && !aborted.Load() && recordCtx.Err() == nil {
		out.Close()
		return program, err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return program, err
	}
	if err := out.Close(); err != nil {
		return program, err
	}
	program.PID = 0
	return program, nil
}

func closeStreamOnContext(ctx context.Context, stream io.Closer) func() {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			_ = stream.Close()
		case <-done:
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

func updateRecordingProgram(recordingPath string, program legacy.Program) error {
	var recording []legacy.Program
	if err := storage.ReadJSON(recordingPath, &recording, "[]"); err != nil {
		return err
	}
	changed := false
	for i := range recording {
		if recording[i].ID == program.ID {
			recording[i] = program
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}
	return storage.WriteJSONAtomic(recordingPath, recording, false)
}

func setProgramRawJSON(program *legacy.Program, key string, value any) {
	if program.Raw == nil {
		program.Raw = map[string]json.RawMessage{}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	program.Raw[key] = data
}

func operatorProgramLogLine(program legacy.Program) string {
	return fmt.Sprintf("#%s %s [%s] %s", program.ID, operatorLegacyISODateTime(program.Start), program.Channel.Name, program.Title)
}

func operatorLegacyISODateTime(timestampMS int64) string {
	return time.UnixMilli(timestampMS).In(time.Local).Format("2006-01-02T15:04:05-0700")
}

func programPriority(cfg *config.Config, program legacy.Program) int {
	if program.IsConflict {
		if cfg.ConflictedPriority != 0 {
			return cfg.ConflictedPriority
		}
		return 1
	}
	if cfg.RecordingPriority != 0 {
		return cfg.RecordingPriority
	}
	return 2
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
				var recording []legacy.Program
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

func handleLowStorage(ctx context.Context, paths Paths, cfg *config.Config, recording, recorded []legacy.Program) ([]legacy.Program, error) {
	if cfg.StorageLowSpaceThresholdMB <= 0 {
		return recorded, nil
	}
	usage, err := getDiskUsage(cfg.RecordedDir)
	if err != nil {
		return recorded, nil
	}
	freeMB := usage.Avail / 1024 / 1024
	if freeMB >= uint64(cfg.StorageLowSpaceThresholdMB) {
		return recorded, nil
	}
	if err := logging.AppendLine(paths.Log, "ALERT: Storage Low Space! (%d MB < %d MB)", freeMB, cfg.StorageLowSpaceThresholdMB); err != nil {
		return recorded, err
	}
	switch cfg.StorageLowSpaceAction {
	case "stop":
		changed := false
		for i := range recording {
			if !recording[i].Abort {
				recording[i].Abort = true
				changed = true
				if paths.Database != "" {
					if err := programstore.Upsert(ctx, paths.Database, paths.Recording, programstore.Recording, recording[i]); err != nil {
						return recorded, err
					}
				}
			}
		}
		if changed {
			if paths.Database == "" {
				if err := programstore.Write(ctx, "", paths.Recording, programstore.Recording, recording); err != nil {
					return recorded, err
				}
			}
			if err := logging.AppendLine(paths.Log, "WRITE: %s", paths.Recording); err != nil {
				return recorded, err
			}
		}
	case "remove":
		if len(recorded) > 0 {
			removed := recorded[0]
			recorded = append([]legacy.Program(nil), recorded[1:]...)
			if removed.Recorded != "" {
				if err := os.Remove(filepath.FromSlash(removed.Recorded)); err != nil && !os.IsNotExist(err) {
					return recorded, err
				}
			}
			if _, err := storage.BackupFile(paths.Recorded); err != nil {
				return recorded, err
			}
			if err := programstore.Remove(ctx, paths.Database, paths.Recorded, programstore.Recorded, removed.ID); err != nil {
				return recorded, err
			}
			if err := logging.AppendLine(paths.Log, "WRITE: %s", paths.Recorded); err != nil {
				return recorded, err
			}
		}
	}
	return recorded, nil
}

func removeProgram(programs []legacy.Program, id string) []legacy.Program {
	out := programs[:0]
	for _, program := range programs {
		if program.ID != id {
			out = append(out, program)
		}
	}
	return out
}

func mergeRecordedProgram(recorded []legacy.Program, completed legacy.Program) []legacy.Program {
	out := make([]legacy.Program, 0, len(recorded)+1)
	replaced := false
	for i, program := range recorded {
		if !replaced && program.ID == completed.ID {
			replaced = true
			if program.Recorded != completed.Recorded {
				program.ID += "-" + strconv.FormatInt(program.Start, 36)
				out = append(out, program)
			}
			out = append(out, recorded[i+1:]...)
			break
		}
		out = append(out, program)
	}
	out = append(out, completed)
	return out
}

func containsProgram(programs []legacy.Program, id string) bool {
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
