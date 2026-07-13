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
	"sync"
	"sync/atomic"
	"time"

	"strata-pvr/internal/config"
	"strata-pvr/internal/database"
	legacy "strata-pvr/internal/domain"
	"strata-pvr/internal/logging"
	"strata-pvr/internal/mirakurun"
	"strata-pvr/internal/programstore"
	"strata-pvr/internal/reservationstore"
	"strata-pvr/internal/system"
)

const recordStartMargin = 15 * time.Second

var getDiskUsage = system.GetDiskUsage

// recordingStateMu serializes short SQLite mutations made by concurrently
// running recordings. Streams themselves remain independent; only their
// state transitions need exclusive database access.
var recordingStateMu sync.Mutex

type StreamSource interface {
	ProgramStream(context.Context, int64, bool) (io.ReadCloser, error)
}

type prioritySetter interface {
	SetPriority(int)
}

type Paths struct {
	Config   string
	Database string
	PID      string
	Log      string
}

type Result struct {
	Started   int
	Completed int
	Failed    int
}

func Run(ctx context.Context, paths Paths, interval time.Duration) error {
	lock, err := acquireProcessLock(paths.PID)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := writePIDFile(paths.PID); err != nil {
		return err
	}
	defer removePIDFile(paths.PID)

	cfg, err := config.Load(paths.Config)
	if err != nil {
		return err
	}
	if err := initializeRuntimeState(paths, cfg); err != nil {
		return err
	}
	db, err := database.Open(ctx, paths.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx = database.WithHandle(ctx, db)

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
	var recordings sync.WaitGroup
	defer recordings.Wait()
	for {
		if err := startPendingRecordings(ctx, paths, cfg, client, time.Now(), &recordings); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// startPendingRecordings starts each due reservation in its own goroutine.
// RunOnce remains synchronous for callers that need to wait for completion,
// while the long-running operator must continue checking reservations while
// previous recordings are still receiving their streams.
func startPendingRecordings(ctx context.Context, paths Paths, cfg *config.Config, source StreamSource, now time.Time, recordings *sync.WaitGroup) error {
	startBefore := now.Add(recordStartMargin).UnixMilli()
	endAfter := now.UnixMilli()
	reserves, err := reservationstore.ReadDue(ctx, paths.Database, startBefore, endAfter)
	if err != nil {
		return err
	}
	recordingIDs, err := programstore.ReadIDs(ctx, paths.Database, programstore.Recording)
	if err != nil {
		return err
	}
	if stopped, err := abortSkippedRecordings(ctx, paths.Database, reserves, recordingIDs); err != nil {
		return err
	} else if stopped > 0 {
		if err := logging.AppendLine(paths.Log, "ABORT: skipped recordings=%d", stopped); err != nil {
			return err
		}
	}
	if cfg.StorageLowSpaceThresholdMB > 0 {
		recording, err := programstore.Read(ctx, paths.Database, programstore.Recording)
		if err != nil {
			return err
		}
		recorded, err := programstore.Read(ctx, paths.Database, programstore.Recorded)
		if err != nil {
			return err
		}
		if _, err = handleLowStorage(ctx, paths, cfg, recording, recorded); err != nil {
			return err
		}
	}
	recordingIDSet := make(map[string]struct{}, len(recordingIDs))
	for _, id := range recordingIDs {
		recordingIDSet[id] = struct{}{}
	}
	pending := make([]legacy.Program, 0)
	for _, reserve := range reserves {
		if !shouldStart(reserve, recordingIDSet, now) {
			continue
		}
		if err := logging.AppendLine(paths.Log, "PREPARE: %s", operatorProgramLogLine(reserve)); err != nil {
			return err
		}
		if err := programstore.Upsert(ctx, paths.Database, programstore.Recording, reserve); err != nil {
			return err
		}
		recordingIDSet[reserve.ID] = struct{}{}
		if err := logCollectionWrite(paths, programstore.Recording); err != nil {
			return err
		}
		if err := logging.AppendLine(paths.Log, "START: %s [%s] %s", reserve.ID, reserve.Channel.Name, reserve.Title); err != nil {
			return err
		}
		pending = append(pending, reserve)
	}
	for _, reserve := range pending {
		recordings.Add(1)
		go func(program legacy.Program) {
			defer recordings.Done()
			finishRecording(ctx, paths, cfg, source, program)
		}(reserve)
	}
	return nil
}

// abortSkippedRecordings applies a later skip action to a recording that has
// already started. The recording goroutine polls its Abort flag and closes its
// stream without waiting for the program to end.
func abortSkippedRecordings(ctx context.Context, databasePath string, reserves []legacy.Program, recordingIDs []string) (int, error) {
	recordingIDSet := make(map[string]struct{}, len(recordingIDs))
	for _, id := range recordingIDs {
		recordingIDSet[id] = struct{}{}
	}
	updated := 0
	recordingStateMu.Lock()
	defer recordingStateMu.Unlock()
	for _, reserve := range reserves {
		if !reserve.IsSkip || reserve.IsManualReserved {
			continue
		}
		if _, ok := recordingIDSet[reserve.ID]; !ok {
			continue
		}
		active, found, err := programstore.ReadByID(ctx, databasePath, programstore.Recording, reserve.ID)
		if err != nil {
			return updated, err
		}
		if !found || active.IsManualReserved || active.Abort {
			continue
		}
		active.Abort = true
		if err := programstore.Upsert(ctx, databasePath, programstore.Recording, active); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}

func finishRecording(ctx context.Context, paths Paths, cfg *config.Config, source StreamSource, program legacy.Program) {
	completed, err := recordProgramWithLog(ctx, paths.Database, paths.Log, cfg, source, program)
	if err != nil {
		recordingStateMu.Lock()
		_ = programstore.Remove(context.WithoutCancel(ctx), paths.Database, programstore.Recording, program.ID)
		_ = logCollectionWrite(paths, programstore.Recording)
		recordingStateMu.Unlock()
		_ = logging.AppendLine(paths.Log, "ERROR: recording %s: %v", program.ID, err)
		return
	}
	recordingStateMu.Lock()
	err = programstore.Complete(context.WithoutCancel(ctx), paths.Database, completed)
	recordingStateMu.Unlock()
	if err != nil {
		_ = logging.AppendLine(paths.Log, "ERROR: complete recording %s: %v", program.ID, err)
		return
	}
	_ = logCollectionWrite(paths, programstore.Recording)
	_ = logCollectionWrite(paths, programstore.Recorded)
	if _, err := reservationstore.Delete(context.WithoutCancel(ctx), paths.Database, completed.ID); err != nil {
		_ = logging.AppendLine(paths.Log, "ERROR: remove completed reserve %s: %v", completed.ID, err)
		return
	}
	_ = logging.AppendLine(paths.Log, "WRITE: %s (reserves)", paths.Database)
	_ = logging.AppendLine(paths.Log, "FIN: %s [%s] %s", completed.ID, completed.Channel.Name, completed.Title)
	_ = logging.AppendLine(paths.Log, "FIN: %s", operatorProgramLogLine(completed))
}

func initializeRuntimeState(paths Paths, cfg *config.Config) error {
	if err := programstore.Write(context.Background(), paths.Database, programstore.Recording, []legacy.Program{}); err != nil {
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
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if string(data) == strconv.Itoa(os.Getpid())+"\n" {
		_ = os.Remove(path)
	}
}

func acquireProcessLock(pidPath string) (*system.ProcessLock, error) {
	if pidPath == "" {
		return nil, nil
	}
	return system.AcquireProcessLock(pidPath + ".lock")
}

func RunOnce(ctx context.Context, paths Paths, cfg *config.Config, source StreamSource, now time.Time) (Result, error) {
	if paths.Database != "" {
		db, err := database.Open(ctx, paths.Database)
		if err != nil {
			return Result{}, err
		}
		defer db.Close()
		ctx = database.WithHandle(ctx, db)
	}
	reserves, err := reservationstore.Read(ctx, paths.Database)
	if err != nil {
		return Result{}, err
	}
	recording, err := programstore.Read(ctx, paths.Database, programstore.Recording)
	if err != nil {
		return Result{}, err
	}
	recorded, err := programstore.Read(ctx, paths.Database, programstore.Recorded)
	if err != nil {
		return Result{}, err
	}
	recorded, err = handleLowStorage(ctx, paths, cfg, recording, recorded)
	if err != nil {
		return Result{}, err
	}
	recordingIDSet := make(map[string]struct{}, len(recording))
	for _, program := range recording {
		recordingIDSet[program.ID] = struct{}{}
	}

	result := Result{}
	for _, reserve := range reserves {
		if !shouldStart(reserve, recordingIDSet, now) {
			continue
		}
		if err := logging.AppendLine(paths.Log, "PREPARE: %s", operatorProgramLogLine(reserve)); err != nil {
			return result, err
		}
		recording = append(recording, reserve)
		if err := programstore.Upsert(ctx, paths.Database, programstore.Recording, reserve); err != nil {
			return result, err
		}
		recordingIDSet[reserve.ID] = struct{}{}
		if err := logCollectionWrite(paths, programstore.Recording); err != nil {
			return result, err
		}
		if err := logging.AppendLine(paths.Log, "START: %s [%s] %s", reserve.ID, reserve.Channel.Name, reserve.Title); err != nil {
			return result, err
		}
		result.Started++

		completed, err := recordProgramWithLog(ctx, paths.Database, paths.Log, cfg, source, reserve)
		recording = removeProgram(recording, reserve.ID)
		delete(recordingIDSet, reserve.ID)
		if err != nil {
			if writeErr := programstore.Remove(ctx, paths.Database, programstore.Recording, reserve.ID); writeErr != nil {
				err = errors.Join(err, writeErr)
			}
			result.Failed++
			return result, err
		}

		recorded = mergeRecordedProgram(recorded, completed)
		if err := programstore.Complete(context.WithoutCancel(ctx), paths.Database, completed); err != nil {
			return result, err
		}
		if err := logCollectionWrite(paths, programstore.Recording); err != nil {
			return result, err
		}
		if err := logCollectionWrite(paths, programstore.Recorded); err != nil {
			return result, err
		}
		reserves = removeProgram(reserves, reserve.ID)
		if _, err := reservationstore.Delete(context.WithoutCancel(ctx), paths.Database, reserve.ID); err != nil {
			return result, err
		}
		if err := logging.AppendLine(paths.Log, "WRITE: %s (reserves)", paths.Database); err != nil {
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

func shouldStart(program legacy.Program, recordingIDs map[string]struct{}, now time.Time) bool {
	if program.IsSkip || program.End <= now.UnixMilli() {
		return false
	}
	if _, ok := recordingIDs[program.ID]; ok {
		return false
	}
	startAt := time.UnixMilli(program.Start).Add(-recordStartMargin)
	return !now.Before(startAt)
}

func recordProgram(ctx context.Context, cfg *config.Config, source StreamSource, program legacy.Program) (legacy.Program, error) {
	return recordProgramWithLog(ctx, ":memory:", "", cfg, source, program)
}

func recordProgramWithLog(ctx context.Context, databasePath, logPath string, cfg *config.Config, source StreamSource, program legacy.Program) (legacy.Program, error) {
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
	aborted, stopAbortMonitor := watchAbortFlag(recordCtx, databasePath, program.ID, cancel, stream)
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
	if err := updateRecordingProgram(recordCtx, databasePath, program); err != nil {
		return program, err
	}
	if logPath != "" {
		if err := logging.AppendLine(logPath, "WRITE: %s (%s)", databasePath, programstore.Recording); err != nil {
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

func updateRecordingProgram(ctx context.Context, databasePath string, program legacy.Program) error {
	recording, err := programstore.Read(ctx, databasePath, programstore.Recording)
	if err != nil {
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
	return programstore.Upsert(ctx, databasePath, programstore.Recording, program)
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

func watchAbortFlag(ctx context.Context, databasePath, programID string, cancel context.CancelFunc, stream io.Closer) (*atomic.Bool, func()) {
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
				program, found, err := programstore.ReadByID(ctx, databasePath, programstore.Recording, programID)
				if err != nil || !found || !program.Abort {
					continue
				}
				aborted.Store(true)
				cancel()
				_ = stream.Close()
				return
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
				if err := programstore.Upsert(ctx, paths.Database, programstore.Recording, recording[i]); err != nil {
					return recorded, err
				}
			}
		}
		if changed {
			if err := logCollectionWrite(paths, programstore.Recording); err != nil {
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
			if err := programstore.Remove(ctx, paths.Database, programstore.Recorded, removed.ID); err != nil {
				return recorded, err
			}
			if err := logCollectionWrite(paths, programstore.Recorded); err != nil {
				return recorded, err
			}
		}
	}
	return recorded, nil
}

func logCollectionWrite(paths Paths, collection string) error {
	return logging.AppendLine(paths.Log, "WRITE: %s (%s)", paths.Database, collection)
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
