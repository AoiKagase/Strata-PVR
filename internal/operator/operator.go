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
	"strings"
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

type completionRollbackKey struct {
	databasePath string
	programID    string
}

type pendingCompletionRollback struct {
	program legacy.Program
}

var pendingCompletionRollbacks = struct {
	sync.Mutex
	programs map[completionRollbackKey]pendingCompletionRollback
}{
	programs: make(map[completionRollbackKey]pendingCompletionRollback),
}

type pendingReservationDeleteKey struct {
	databasePath string
	programID    string
}

var pendingReservationDeletes = struct {
	sync.Mutex
	ids map[pendingReservationDeleteKey]struct{}
}{
	ids: make(map[pendingReservationDeleteKey]struct{}),
}

type pendingRecordingRemovalKey struct {
	databasePath string
	programID    string
}

var pendingRecordingRemovals = struct {
	sync.Mutex
	ids map[pendingRecordingRemovalKey]struct{}
}{
	ids: make(map[pendingRecordingRemovalKey]struct{}),
}

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
	retryPendingRecordingRemovals(ctx, paths.Database)
	recordingStateMu.Lock()
	retryPendingCompletionRollbacks(ctx, paths.Database)
	retryPendingCompletions(ctx, paths.Database)
	recordingStateMu.Unlock()
	retryPendingReservationDeletes(ctx, paths.Database)
	startMargin := recordingStartMargin(cfg)
	endMargin := recordingEndMargin(cfg)
	startBefore := now.Add(startMargin).UnixMilli()
	endAfter := now.UnixMilli()
	if endMargin < 0 {
		// ReadDue compares the guide's unadjusted end time. Shift the query
		// boundary so a negative margin shortens the late-start window too.
		endAfter = now.Add(-endMargin).UnixMilli()
	}
	recordingIDs, err := programstore.ReadIDs(ctx, paths.Database, programstore.Recording)
	if err != nil {
		return err
	}
	recordedIDs, err := programstore.ReadIDs(ctx, paths.Database, programstore.Recorded)
	if err != nil {
		return err
	}
	if err := cleanupRecordedReservations(ctx, paths.Database, recordedIDs); err != nil {
		return err
	}
	reserves, err := reservationstore.ReadDue(ctx, paths.Database, startBefore, endAfter)
	if err != nil {
		return err
	}
	abortReserves, err := reservationstore.ReadByIDs(ctx, paths.Database, recordingIDs)
	if err != nil {
		return err
	}
	if stopped, err := abortSkippedRecordings(ctx, paths.Database, abortReserves, recordingIDs); err != nil {
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
	recordingIDSet := make(map[string]struct{}, len(recordingIDs)+len(recordedIDs))
	for _, id := range recordingIDs {
		recordingIDSet[id] = struct{}{}
	}
	for _, id := range recordedIDs {
		recordingIDSet[id] = struct{}{}
	}
	pending := make([]legacy.Program, 0)
	for _, reserve := range reserves {
		if !shouldStartWithMargins(reserve, recordingIDSet, now, startMargin, endMargin) {
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
		if err := programstore.SetAbort(ctx, databasePath, programstore.Recording, reserve.ID, true); err != nil {
			if errors.Is(err, programstore.ErrProgramFinalizing) {
				continue
			}
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
		if removeErr := programstore.Remove(context.WithoutCancel(ctx), paths.Database, programstore.Recording, program.ID); removeErr != nil {
			queueRecordingRemoval(paths.Database, program.ID)
		}
		_ = logCollectionWrite(paths, programstore.Recording)
		recordingStateMu.Unlock()
		_ = logging.AppendLine(paths.Log, "ERROR: recording %s: %v", program.ID, err)
		return
	}
	recordingStateMu.Lock()
	err = programstore.Complete(context.WithoutCancel(ctx), paths.Database, completed)
	recordingStateMu.Unlock()
	if err != nil {
		_ = logging.AppendLine(paths.Log, "ERROR: complete recording %s: %v", completed.ID, err)
		return
	}
	_ = logCollectionWrite(paths, programstore.Recording)
	_ = logCollectionWrite(paths, programstore.Recorded)
	if _, err := reservationstore.Delete(context.WithoutCancel(ctx), paths.Database, completed.ID); err != nil {
		queueReservationDelete(paths.Database, completed.ID)
		_ = logging.AppendLine(paths.Log, "ERROR: remove completed reserve %s: %v", completed.ID, err)
		return
	}
	_ = logging.AppendLine(paths.Log, "WRITE: %s (reserves)", paths.Database)
	_ = logging.AppendLine(paths.Log, "FIN: %s [%s] %s", completed.ID, completed.Channel.Name, completed.Title)
	_ = logging.AppendLine(paths.Log, "FIN: %s", operatorProgramLogLine(completed))
}

func initializeRuntimeState(paths Paths, cfg *config.Config) error {
	recordingStateMu.Lock()
	retryPendingCompletions(context.Background(), paths.Database)
	recordingStateMu.Unlock()
	recording, err := programstore.Read(context.Background(), paths.Database, programstore.Recording)
	if err != nil {
		return err
	}
	pending := make([]legacy.Program, 0, len(recording))
	for _, program := range recording {
		if program.Recorded != "" && program.PID == 0 && programstore.CompletionClaimed(program) {
			pending = append(pending, program)
		}
	}
	if err := programstore.Write(context.Background(), paths.Database, programstore.Recording, pending); err != nil {
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
	retryPendingRecordingRemovals(ctx, paths.Database)
	retryPendingCompletions(ctx, paths.Database)
	retryPendingReservationDeletes(ctx, paths.Database)
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
	if err := cleanupRecordedReservations(ctx, paths.Database, programIDs(recorded)); err != nil {
		return Result{}, err
	}
	reserves, err = reservationstore.Read(ctx, paths.Database)
	if err != nil {
		return Result{}, err
	}
	recorded, err = handleLowStorage(ctx, paths, cfg, recording, recorded)
	if err != nil {
		return Result{}, err
	}
	recordingIDSet := make(map[string]struct{}, len(recording)+len(recorded))
	for _, program := range recording {
		recordingIDSet[program.ID] = struct{}{}
	}
	for _, program := range recorded {
		recordingIDSet[program.ID] = struct{}{}
	}

	result := Result{}
	for _, reserve := range reserves {
		if !shouldStartWithMargins(reserve, recordingIDSet, now, recordingStartMargin(cfg), recordingEndMargin(cfg)) {
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
			if writeErr := programstore.Remove(context.WithoutCancel(ctx), paths.Database, programstore.Recording, reserve.ID); writeErr != nil {
				queueRecordingRemoval(paths.Database, reserve.ID)
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
			queueReservationDelete(paths.Database, reserve.ID)
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

func rollbackCompletionClaim(ctx context.Context, databasePath string, program legacy.Program) error {
	if databasePath == ":memory:" {
		return nil
	}
	if err := programstore.Remove(ctx, databasePath, programstore.Recording, program.ID); err == nil {
		return nil
	} else {
		rollbackErr := err
		if clearErr := programstore.ClearCompletionClaim(ctx, databasePath, program.ID); clearErr != nil {
			rollbackErr = errors.Join(rollbackErr, clearErr)
		}
		queueCompletionClaimRollback(databasePath, program)
		return rollbackErr
	}
}

func queueCompletionClaimRollback(databasePath string, program legacy.Program) {
	if databasePath == ":memory:" || databasePath == "" {
		return
	}
	pendingCompletionRollbacks.Lock()
	pendingCompletionRollbacks.programs[completionRollbackKey{databasePath: databasePath, programID: program.ID}] = pendingCompletionRollback{program: program}
	pendingCompletionRollbacks.Unlock()
}

func retryPendingCompletionRollbacks(ctx context.Context, databasePath string) {
	if databasePath == ":memory:" || databasePath == "" {
		return
	}
	pendingCompletionRollbacks.Lock()
	entries := make(map[completionRollbackKey]pendingCompletionRollback)
	for key, entry := range pendingCompletionRollbacks.programs {
		if key.databasePath == databasePath {
			entries[key] = entry
		}
	}
	pendingCompletionRollbacks.Unlock()
	for key, entry := range entries {
		program := entry.program
		current, found, err := programstore.ReadByID(ctx, key.databasePath, programstore.Recording, key.programID)
		if err != nil {
			continue
		}
		if !found {
			pendingCompletionRollbacks.Lock()
			delete(pendingCompletionRollbacks.programs, key)
			pendingCompletionRollbacks.Unlock()
			continue
		}
		expectedToken := programstore.CompletionClaimToken(program)
		currentToken := programstore.CompletionClaimToken(current)
		if current.Recorded != program.Recorded || current.PID != 0 || (expectedToken != "" && currentToken != "" && currentToken != expectedToken) {
			pendingCompletionRollbacks.Lock()
			delete(pendingCompletionRollbacks.programs, key)
			pendingCompletionRollbacks.Unlock()
			continue
		}
		if err := programstore.Remove(ctx, key.databasePath, programstore.Recording, key.programID); err != nil {
			continue
		}
		pendingCompletionRollbacks.Lock()
		delete(pendingCompletionRollbacks.programs, key)
		pendingCompletionRollbacks.Unlock()
	}
}

func retryPendingCompletions(ctx context.Context, databasePath string) {
	if databasePath == "" || databasePath == ":memory:" {
		return
	}
	recording, err := programstore.Read(ctx, databasePath, programstore.Recording)
	if err != nil {
		return
	}
	for _, program := range recording {
		if program.Recorded == "" || program.PID != 0 || !programstore.CompletionClaimed(program) {
			continue
		}
		_ = programstore.Complete(context.WithoutCancel(ctx), databasePath, program)
	}
}

func programIDs(programs []legacy.Program) []string {
	ids := make([]string, 0, len(programs))
	for _, program := range programs {
		ids = append(ids, program.ID)
	}
	return ids
}

func queueRecordingRemoval(databasePath, programID string) {
	if databasePath == "" || programID == "" {
		return
	}
	pendingRecordingRemovals.Lock()
	pendingRecordingRemovals.ids[pendingRecordingRemovalKey{databasePath: databasePath, programID: programID}] = struct{}{}
	pendingRecordingRemovals.Unlock()
}

func retryPendingRecordingRemovals(ctx context.Context, databasePath string) {
	if databasePath == "" {
		return
	}
	pendingRecordingRemovals.Lock()
	entries := make([]pendingRecordingRemovalKey, 0)
	for key := range pendingRecordingRemovals.ids {
		if key.databasePath == databasePath {
			entries = append(entries, key)
		}
	}
	pendingRecordingRemovals.Unlock()
	for _, key := range entries {
		if err := programstore.Remove(ctx, key.databasePath, programstore.Recording, key.programID); err != nil {
			continue
		}
		pendingRecordingRemovals.Lock()
		delete(pendingRecordingRemovals.ids, key)
		pendingRecordingRemovals.Unlock()
	}
}

func queueReservationDelete(databasePath, programID string) {
	if databasePath == "" || programID == "" {
		return
	}
	pendingReservationDeletes.Lock()
	pendingReservationDeletes.ids[pendingReservationDeleteKey{databasePath: databasePath, programID: programID}] = struct{}{}
	pendingReservationDeletes.Unlock()
}

func retryPendingReservationDeletes(ctx context.Context, databasePath string) {
	if databasePath == "" {
		return
	}
	pendingReservationDeletes.Lock()
	entries := make([]pendingReservationDeleteKey, 0)
	for key := range pendingReservationDeletes.ids {
		if key.databasePath == databasePath {
			entries = append(entries, key)
		}
	}
	pendingReservationDeletes.Unlock()
	for _, key := range entries {
		if _, err := reservationstore.Delete(ctx, key.databasePath, key.programID); err != nil {
			continue
		}
		pendingReservationDeletes.Lock()
		delete(pendingReservationDeletes.ids, key)
		pendingReservationDeletes.Unlock()
	}
}

func cleanupRecordedReservations(ctx context.Context, databasePath string, recordedIDs []string) error {
	if len(recordedIDs) == 0 {
		return nil
	}
	reserves, err := reservationstore.ReadByIDs(ctx, databasePath, recordedIDs)
	if err != nil {
		return err
	}
	for _, reserve := range reserves {
		if _, err := reservationstore.Delete(context.WithoutCancel(ctx), databasePath, reserve.ID); err != nil {
			queueReservationDelete(databasePath, reserve.ID)
		}
	}
	return nil
}

func shouldStart(program legacy.Program, recordingIDs map[string]struct{}, now time.Time) bool {
	return shouldStartWithMargin(program, recordingIDs, now, recordStartMargin)
}

func shouldStartWithMargin(program legacy.Program, recordingIDs map[string]struct{}, now time.Time, startMargin time.Duration) bool {
	return shouldStartWithMargins(program, recordingIDs, now, startMargin, 0)
}

func shouldStartWithMargins(program legacy.Program, recordingIDs map[string]struct{}, now time.Time, startMargin, endMargin time.Duration) bool {
	if program.IsSkip || program.End <= now.UnixMilli() {
		return false
	}
	if endMargin < 0 && !time.UnixMilli(program.End).Add(endMargin).After(now) {
		return false
	}
	if _, ok := recordingIDs[program.ID]; ok {
		return false
	}
	startAt := time.UnixMilli(program.Start).Add(-startMargin)
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
	endReached := atomic.Bool{}
	var endTimer *time.Timer
	endMargin := recordingEndMargin(cfg)
	endAt := time.UnixMilli(program.End).Add(endMargin)
	if endAt.After(time.Now()) {
		endTimer = time.AfterFunc(time.Until(endAt), func() {
			endReached.Store(true)
			cancel()
		})
		defer endTimer.Stop()
	} else if endMargin != 0 {
		endReached.Store(true)
		cancel()
	}
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
	stopAbortMonitor := watchAbortFlag(recordCtx, databasePath, program.ID, cancel, stream)
	defer stopAbortMonitor()

	format := cfg.RecordedFormat
	if program.RecordedFormat != "" {
		format = program.RecordedFormat
	}
	relativeName := legacy.FormatRecordedName(program, format)
	finalPath, err := recordingOutputPath(cfg.RecordedDir, relativeName)
	if err != nil {
		return program, err
	}
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
	// Multiple recordings can reach a state transition at the same instant.
	// Keep the read-modify-write transaction below single-writer: otherwise a
	// second SQLite connection may fail with SQLITE_BUSY_SNAPSHOT after it has
	// read the previous version of the recording row.
	recordingStateMu.Lock()
	err = updateRecordingProgram(context.WithoutCancel(recordCtx), databasePath, program)
	recordingStateMu.Unlock()
	if err != nil {
		return program, err
	}
	if logPath != "" {
		if err := logging.AppendLine(logPath, "WRITE: %s (%s)", databasePath, programstore.Recording); err != nil {
			return program, err
		}
	}
	partPath := finalPath + ".part"
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(partPath)
		}
	}()
	out, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return program, err
	}
	if _, err := io.Copy(out, stream); err != nil {
		if recordCtx.Err() != nil && !endReached.Load() {
			out.Close()
			return program, recordCtx.Err()
		}
		if !endReached.Load() {
			out.Close()
			return program, err
		}
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return program, err
	}
	if err := out.Close(); err != nil {
		return program, err
	}
	if recordCtx.Err() != nil && !endReached.Load() {
		return program, recordCtx.Err()
	}
	abortRequested, err := recordingAbortRequested(recordCtx, databasePath, program.ID)
	if err != nil {
		return program, err
	}
	if abortRequested {
		return program, context.Canceled
	}
	if databasePath != ":memory:" {
		recordingStateMu.Lock()
		claimed, claimToken, err := programstore.ClaimCompletionWithToken(context.WithoutCancel(recordCtx), databasePath, program)
		recordingStateMu.Unlock()
		if err != nil {
			return program, err
		}
		if !claimed {
			return program, programstore.ErrProgramNotFound
		}
		if program.Raw == nil {
			program.Raw = make(map[string]json.RawMessage)
		}
		if encodedToken, err := json.Marshal(claimToken); err == nil {
			program.Raw["_strataFinalizing"] = encodedToken
		}
	}
	if err := replaceRecordingOutput(partPath, finalPath); err != nil {
		if databasePath != ":memory:" {
			if rollbackErr := rollbackCompletionClaim(context.WithoutCancel(recordCtx), databasePath, program); rollbackErr != nil {
				err = errors.Join(err, rollbackErr)
			}
		}
		return program, err
	}
	renamed = true
	program.PID = 0
	return program, nil
}

func recordingOutputPath(recordingDir, relativeName string) (string, error) {
	if relativeName == "" || filepath.IsAbs(relativeName) || filepath.VolumeName(relativeName) != "" {
		return "", fmt.Errorf("invalid recording filename %q", relativeName)
	}
	base, err := filepath.Abs(recordingDir)
	if err != nil {
		return "", fmt.Errorf("resolve recording directory: %w", err)
	}
	path, err := filepath.Abs(filepath.Join(base, filepath.FromSlash(relativeName)))
	if err != nil {
		return "", fmt.Errorf("resolve recording filename: %w", err)
	}
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("recording filename escapes recording directory: %q", relativeName)
	}
	return path, nil
}

func recordingStartMargin(cfg *config.Config) time.Duration {
	if cfg == nil {
		return recordStartMargin
	}
	if !cfg.RecordingStartMarginSet && cfg.RecordingStartMargin == 0 {
		return recordStartMargin
	}
	return marginDuration(cfg.RecordingStartMargin)
}

func recordingEndMargin(cfg *config.Config) time.Duration {
	if cfg == nil {
		return 0
	}
	return marginDuration(cfg.RecordingEndMargin)
}

func marginDuration(seconds int) time.Duration {
	maxDuration := time.Duration(1<<63 - 1)
	maxSeconds := int64(maxDuration / time.Second)
	seconds64 := int64(seconds)
	if seconds64 > maxSeconds {
		return maxDuration
	}
	if seconds64 < -maxSeconds {
		return -maxDuration
	}
	return time.Duration(seconds64) * time.Second
}

func recordingAbortRequested(ctx context.Context, databasePath, programID string) (bool, error) {
	if databasePath == ":memory:" {
		return false, nil
	}
	program, found, err := programstore.ReadByID(context.WithoutCancel(ctx), databasePath, programstore.Recording, programID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, programstore.ErrProgramNotFound
	}
	return program.Abort, nil
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
	return programstore.Update(ctx, databasePath, programstore.Recording, program.ID, func(current legacy.Program) (legacy.Program, error) {
		current.Recorded = program.Recorded
		current.PID = program.PID
		if len(program.Raw) > 0 {
			if current.Raw == nil {
				current.Raw = make(map[string]json.RawMessage)
			}
			for _, key := range []string{"priority", "tuner", "command"} {
				if value, ok := program.Raw[key]; ok {
					current.Raw[key] = value
				}
			}
		}
		return current, nil
	})
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

func watchAbortFlag(ctx context.Context, databasePath, programID string, cancel context.CancelFunc, stream io.Closer) func() {
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
				cancel()
				_ = stream.Close()
				return
			}
		}
	}()
	return func() {
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
				if err := programstore.SetAbort(ctx, paths.Database, programstore.Recording, recording[i].ID, true); err != nil {
					if errors.Is(err, programstore.ErrProgramFinalizing) {
						continue
					}
					return recorded, err
				}
				recording[i].Abort = true
				changed = true
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
