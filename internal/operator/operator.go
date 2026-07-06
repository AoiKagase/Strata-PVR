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
	"chinachu-go/internal/logging"
	"chinachu-go/internal/mirakurun"
	"chinachu-go/internal/storage"
	"chinachu-go/internal/system"
)

const recordStartMargin = 15 * time.Second

var getDiskUsage = system.GetDiskUsage
var sendmailPath = "/usr/sbin/sendmail"
var lowStorageNow = time.Now
var lowStorageLastNotified time.Time

const lowStorageNotifyInterval = 3 * time.Hour

type StreamSource interface {
	ProgramStream(context.Context, int64, bool) (io.ReadCloser, error)
}

type prioritySetter interface {
	SetPriority(int)
}

type Paths struct {
	Config    string
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
	if err := system.DropPrivileges(cfg.UID, cfg.GID); err != nil {
		return err
	}
	if err := writePIDFile(paths.PID); err != nil {
		return err
	}
	defer removePIDFile(paths.PID)

	client, err := mirakurun.New(cfg.EffectiveMirakurunPath())
	if err != nil {
		return err
	}
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
	var err error
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
		if err := storage.WriteJSONAtomic(paths.Recording, recording, false); err != nil {
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
		if writeErr := storage.WriteJSONAtomic(paths.Recording, recording, false); writeErr != nil && err == nil {
			err = writeErr
		}
		if err != nil {
			result.Failed++
			return result, err
		}

		recorded = mergeRecordedProgram(recorded, completed)
		if err := storage.WriteJSONAtomic(paths.Recorded, recorded, false); err != nil {
			return result, err
		}
		if completed.IsManualReserved {
			reserves = removeProgram(reserves, reserve.ID)
			if err := storage.WriteJSONAtomic(paths.Reserves, reserves, false); err != nil {
				return result, err
			}
			if err := logging.AppendLine(paths.Log, "WRITE: %s", paths.Reserves); err != nil {
				return result, err
			}
		}
		if err := runRecordedCommand(ctx, paths.Log, cfg.RecordedCommand, completed); err != nil {
			result.Failed++
			return result, err
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
	return recordProgramWithLog(ctx, recordingPath, "", cfg, source, program)
}

func recordProgramWithLog(ctx context.Context, recordingPath, logPath string, cfg *config.Config, source StreamSource, program chinachu.Program) (chinachu.Program, error) {
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
	aborted, stopAbortMonitor := watchAbortFlag(recordCtx, recordingPath, program.ID, cancel, stream)
	defer stopAbortMonitor()

	format := cfg.RecordedFormat
	if program.RecordedFormat != "" {
		format = program.RecordedFormat
	}
	relativeName := chinachu.FormatRecordedName(program, format)
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
	program.PID = 0
	return program, nil
}

func updateRecordingProgram(recordingPath string, program chinachu.Program) error {
	var recording []chinachu.Program
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

func setProgramRawJSON(program *chinachu.Program, key string, value any) {
	if program.Raw == nil {
		program.Raw = map[string]json.RawMessage{}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	program.Raw[key] = data
}

func operatorProgramLogLine(program chinachu.Program) string {
	return fmt.Sprintf("#%s %s [%s] %s", program.ID, operatorLegacyISODateTime(program.Start), program.Channel.Name, program.Title)
}

func operatorLegacyISODateTime(timestampMS int64) string {
	return time.UnixMilli(timestampMS).In(time.Local).Format("2006-01-02T15:04:05-0700")
}

func programPriority(cfg *config.Config, program chinachu.Program) int {
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

func runRecordedCommand(ctx context.Context, logPath, command string, program chinachu.Program) error {
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
	if logPath != "" {
		if err := logging.AppendLine(logPath, "SPAWN: %s (pid=%d)", command, cmd.Process.Pid); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return err
		}
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func handleLowStorage(ctx context.Context, paths Paths, cfg *config.Config, recording, recorded []chinachu.Program) ([]chinachu.Program, error) {
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
	if cfg.StorageLowSpaceCommand != "" {
		cmd := exec.CommandContext(ctx, cfg.StorageLowSpaceCommand)
		if err := cmd.Start(); err != nil {
			return recorded, err
		}
		if err := logging.AppendLine(paths.Log, "SPAWN: %s (pid=%d)", cfg.StorageLowSpaceCommand, cmd.Process.Pid); err != nil {
			return recorded, err
		}
		go func() { _ = cmd.Wait() }()
	}
	switch cfg.StorageLowSpaceAction {
	case "stop":
		changed := false
		for i := range recording {
			if !recording[i].Abort {
				recording[i].Abort = true
				changed = true
			}
		}
		if changed {
			if err := storage.WriteJSONAtomic(paths.Recording, recording, false); err != nil {
				return recorded, err
			}
			if err := logging.AppendLine(paths.Log, "WRITE: %s", paths.Recording); err != nil {
				return recorded, err
			}
		}
	case "remove":
		if len(recorded) > 0 {
			removed := recorded[0]
			recorded = append([]chinachu.Program(nil), recorded[1:]...)
			if removed.Recorded != "" {
				if err := os.Remove(filepath.FromSlash(removed.Recorded)); err != nil && !os.IsNotExist(err) {
					return recorded, err
				}
			}
			if _, err := storage.BackupFile(paths.Recorded); err != nil {
				return recorded, err
			}
			if err := storage.WriteJSONAtomic(paths.Recorded, recorded, false); err != nil {
				return recorded, err
			}
			if err := logging.AppendLine(paths.Log, "WRITE: %s", paths.Recorded); err != nil {
				return recorded, err
			}
		}
	}
	if shouldSendLowStorageNotification(cfg.StorageLowSpaceNotifyTo) {
		if err := sendLowStorageNotification(ctx, cfg.StorageLowSpaceNotifyTo, freeMB, cfg.StorageLowSpaceThresholdMB); err != nil {
			if logErr := logging.AppendLine(paths.Log, "ERROR: %v", err); logErr != nil {
				return recorded, logErr
			}
		}
	}
	return recorded, nil
}

func shouldSendLowStorageNotification(to string) bool {
	if to == "" {
		return false
	}
	now := lowStorageNow()
	if lowStorageLastNotified.IsZero() || now.Sub(lowStorageLastNotified) > lowStorageNotifyInterval {
		lowStorageLastNotified = now
		return true
	}
	return false
}

func sendLowStorageNotification(ctx context.Context, to string, freeMB uint64, thresholdMB int) error {
	if to == "" {
		return nil
	}
	message := fmt.Sprintf(
		"From: Chinachu <chinachu@localhost>\nTo: %s\nSubject: [Chinachu] ALERT: Storage Low Space!\n\nCurrent Free Space is %d MB.\nThreshold is %d MB.\n",
		to,
		freeMB,
		thresholdMB,
	)
	cmd := exec.CommandContext(ctx, sendmailPath, "-t")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if _, err := io.WriteString(stdin, message); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return err
	}
	if err := stdin.Close(); err != nil {
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
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

func mergeRecordedProgram(recorded []chinachu.Program, completed chinachu.Program) []chinachu.Program {
	out := make([]chinachu.Program, 0, len(recorded)+1)
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
