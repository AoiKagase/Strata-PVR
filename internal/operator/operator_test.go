package operator

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"strata-pvr/internal/config"
	"strata-pvr/internal/legacy"
	"strata-pvr/internal/programstore"
	"strata-pvr/internal/reservationstore"
	"strata-pvr/internal/storage"
	"strata-pvr/internal/system"
)

type fakeStreamer struct {
	id     int64
	decode bool
	body   string
}

func (f *fakeStreamer) ProgramStream(_ context.Context, id int64, decode bool) (io.ReadCloser, error) {
	f.id = id
	f.decode = decode
	return io.NopCloser(strings.NewReader(f.body)), nil
}

type priorityStreamer struct {
	fakeStreamer
	priority int
}

func (f *priorityStreamer) SetPriority(priority int) {
	f.priority = priority
}

func TestInitializeRuntimeStateClearsRecordingAndCreatesRecordedDir(t *testing.T) {
	dir := t.TempDir()
	paths := Paths{
		Recording: filepath.Join(dir, "data", "recording.json"),
		Log:       filepath.Join(dir, "log", "operator"),
	}
	if err := storage.WriteJSONAtomic(paths.Recording, []legacy.Program{{ID: "stale"}}, false); err != nil {
		t.Fatal(err)
	}
	recordedDir := filepath.Join(dir, "recorded")
	if err := initializeRuntimeState(paths, &config.Config{RecordedDir: recordedDir}); err != nil {
		t.Fatal(err)
	}
	var recording []legacy.Program
	if err := storage.ReadJSON(paths.Recording, &recording, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(recording) != 0 {
		t.Fatalf("recording state was not cleared: %#v", recording)
	}
	if info, err := os.Stat(recordedDir); err != nil || !info.IsDir() {
		t.Fatalf("recordedDir was not created: info=%v err=%v", info, err)
	}
	logData, err := os.ReadFile(paths.Log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "MKDIR: "+recordedDir) {
		t.Fatalf("operator log missing MKDIR line: %s", string(logData))
	}
}

type abortableStreamer struct {
	stream *abortableReadCloser
}

func (f *abortableStreamer) ProgramStream(context.Context, int64, bool) (io.ReadCloser, error) {
	f.stream = newAbortableReadCloser()
	return f.stream, nil
}

type abortableReadCloser struct {
	closed chan struct{}
	once   sync.Once
}

func newAbortableReadCloser() *abortableReadCloser {
	return &abortableReadCloser{closed: make(chan struct{})}
}

func (r *abortableReadCloser) Read(p []byte) (int, error) {
	select {
	case <-r.closed:
		return 0, io.EOF
	case <-time.After(10 * time.Millisecond):
		p[0] = 'x'
		return 1, nil
	}
}

func (r *abortableReadCloser) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

func assertLogSubsequence(t *testing.T, logData []byte, want ...string) {
	t.Helper()
	logText := string(logData)
	cursor := 0
	for _, entry := range want {
		index := strings.Index(logText[cursor:], entry)
		if index < 0 {
			t.Fatalf("operator log missing %q after offset %d: %s", entry, cursor, logText)
		}
		cursor += index + len(entry)
	}
}

func TestRunOnceRecordsDueProgram(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	paths := Paths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
		Log:       filepath.Join(dir, "log", "operator"),
	}
	program := legacy.Program{
		ID:       "21i3v9",
		Title:    "Test/Program",
		Start:    now.Add(10 * time.Second).UnixMilli(),
		End:      now.Add(time.Hour).UnixMilli(),
		Category: "anime",
		Channel:  legacy.Channel{Type: "GR", Channel: "27", Name: "Service"},
	}
	if err := storage.WriteJSONAtomic(paths.Reserves, []legacy.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		RecordedDir:    filepath.Join(dir, "recorded"),
		RecordedFormat: "<id>-<title>.m2ts",
	}
	streamer := &fakeStreamer{body: "tsdata"}
	result, err := RunOnce(context.Background(), paths, cfg, streamer, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Started != 1 || result.Completed != 1 || result.Failed != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if streamer.id != 123456789 || !streamer.decode {
		t.Fatalf("unexpected stream request id=%d decode=%v", streamer.id, streamer.decode)
	}
	var reserves []legacy.Program
	if err := storage.ReadJSON(paths.Reserves, &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 1 || reserves[0].ID != program.ID {
		t.Fatalf("auto reserve should remain like legacy operator: %#v", reserves)
	}
	var recording []legacy.Program
	if err := storage.ReadJSON(paths.Recording, &recording, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(recording) != 0 {
		t.Fatalf("recording not cleared: %#v", recording)
	}
	var recorded []legacy.Program
	if err := storage.ReadJSON(paths.Recorded, &recorded, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || recorded[0].Recorded == "" {
		t.Fatalf("recorded entry missing: %#v", recorded)
	}
	data, err := os.ReadFile(filepath.FromSlash(recorded[0].Recorded))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "tsdata" {
		t.Fatalf("recorded data = %q", data)
	}
	logData, err := os.ReadFile(paths.Log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "START: 21i3v9") || !strings.Contains(string(logData), "FIN: 21i3v9") {
		t.Fatalf("operator log missing expected lines: %s", string(logData))
	}
	for _, want := range []string{
		"PREPARE: #21i3v9 ",
		"RECORD: #21i3v9 ",
		"STREAM: ",
		"WRITE: " + paths.Recording,
		"WRITE: " + paths.Recorded,
		"FIN: #21i3v9 ",
	} {
		if !strings.Contains(string(logData), want) {
			t.Fatalf("operator log missing %q: %s", want, string(logData))
		}
	}
	if got := strings.Count(string(logData), "WRITE: "+paths.Recording); got < 2 {
		t.Fatalf("operator log should include initial and completion recording writes, got %d: %s", got, string(logData))
	}
	assertLogSubsequence(t, logData,
		"PREPARE: #21i3v9 ",
		"WRITE: "+paths.Recording,
		"START: 21i3v9",
		"RECORD: #21i3v9 ",
		"STREAM: ",
		"WRITE: "+paths.Recording,
		"WRITE: "+paths.Recording,
		"WRITE: "+paths.Recorded,
		"FIN: 21i3v9",
		"FIN: #21i3v9 ",
	)
}

func TestRunOnceMovesProgramToRecordedInStrataDatabase(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	paths := Paths{
		Database: filepath.Join(dir, "data", "strata.db"), Reserves: filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"), Recorded: filepath.Join(dir, "data", "recorded.json"), Log: filepath.Join(dir, "log", "operator"),
	}
	if err := os.MkdirAll(filepath.Dir(paths.Database), 0o755); err != nil {
		t.Fatal(err)
	}
	program := legacy.Program{ID: "21i3v9", Title: "Database", Start: now.UnixMilli(), End: now.Add(time.Hour).UnixMilli(), Channel: legacy.Channel{Type: "GR", Channel: "27"}, IsManualReserved: true}
	if err := reservationstore.Upsert(context.Background(), paths.Database, paths.Reserves, program); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{RecordedDir: filepath.Join(dir, "recorded"), RecordedFormat: "<id>.m2ts"}
	result, err := RunOnce(context.Background(), paths, cfg, &fakeStreamer{body: "database-ts"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Completed != 1 {
		t.Fatalf("result = %#v", result)
	}
	recording, err := programstore.Read(context.Background(), paths.Database, paths.Recording, programstore.Recording)
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := programstore.Read(context.Background(), paths.Database, paths.Recorded, programstore.Recorded)
	if err != nil {
		t.Fatal(err)
	}
	reserves, err := reservationstore.Read(context.Background(), paths.Database, paths.Reserves)
	if err != nil {
		t.Fatal(err)
	}
	if len(recording) != 0 || len(recorded) != 1 || recorded[0].ID != program.ID || len(reserves) != 0 {
		t.Fatalf("recording=%#v recorded=%#v reserves=%#v", recording, recorded, reserves)
	}
	for _, path := range []string{paths.Recording, paths.Recorded, paths.Reserves} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("compatibility JSON %s unexpectedly written: %v", path, err)
		}
	}
}

func TestRunOnceRemovesCompletedManualReserveLikeLegacy(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	paths := Paths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
		Log:       filepath.Join(dir, "log", "operator"),
	}
	program := legacy.Program{
		ID:               "21i3v9",
		Title:            "Manual",
		Start:            now.UnixMilli(),
		End:              now.Add(time.Hour).UnixMilli(),
		Channel:          legacy.Channel{Type: "GR", Channel: "27", Name: "Service"},
		IsManualReserved: true,
	}
	if err := storage.WriteJSONAtomic(paths.Reserves, []legacy.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{RecordedDir: filepath.Join(dir, "recorded"), RecordedFormat: "<id>.m2ts"}
	result, err := RunOnce(context.Background(), paths, cfg, &fakeStreamer{body: "tsdata"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Completed != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	var reserves []legacy.Program
	if err := storage.ReadJSON(paths.Reserves, &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 0 {
		t.Fatalf("manual reserve was not removed: %#v", reserves)
	}
	logData, err := os.ReadFile(paths.Log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "WRITE: "+paths.Reserves) {
		t.Fatalf("manual reserve write log missing: %s", string(logData))
	}
}

func TestRecordProgramSetsMirakurunPriority(t *testing.T) {
	dir := t.TempDir()
	recordingPath := filepath.Join(dir, "data", "recording.json")
	if err := storage.WriteJSONAtomic(recordingPath, []legacy.Program{}, false); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		RecordedDir:        filepath.Join(dir, "recorded"),
		RecordedFormat:     "<id>.m2ts",
		RecordingPriority:  5,
		ConflictedPriority: 1,
	}
	program := legacy.Program{ID: "1", Title: "Priority"}
	normal := &priorityStreamer{fakeStreamer: fakeStreamer{body: "normal"}}
	if _, err := recordProgram(context.Background(), recordingPath, cfg, normal, program); err != nil {
		t.Fatal(err)
	}
	if normal.priority != 5 {
		t.Fatalf("normal priority = %d", normal.priority)
	}

	conflicted := &priorityStreamer{fakeStreamer: fakeStreamer{body: "conflict"}}
	program.IsConflict = true
	if _, err := recordProgram(context.Background(), recordingPath, cfg, conflicted, program); err != nil {
		t.Fatal(err)
	}
	if conflicted.priority != 1 {
		t.Fatalf("conflicted priority = %d", conflicted.priority)
	}
}

func TestOperatorLegacyISODateTimeUsesDateformatOffset(t *testing.T) {
	oldLocal := time.Local
	time.Local = time.FixedZone("JST", 9*60*60)
	defer func() { time.Local = oldLocal }()

	ts := time.Date(2024, 7, 1, 20, 30, 45, 0, time.Local).UnixMilli()
	if got := operatorLegacyISODateTime(ts); got != "2024-07-01T20:30:45+0900" {
		t.Fatalf("operatorLegacyISODateTime = %q", got)
	}
}

func TestRunOnceRecordsConflictsWithConflictedPriority(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	paths := Paths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
		Log:       filepath.Join(dir, "log", "operator"),
	}
	reserves := []legacy.Program{
		{ID: "a", Start: now.Add(time.Minute).UnixMilli(), End: now.Add(time.Hour).UnixMilli()},
		{ID: "b", Start: now.UnixMilli(), End: now.Add(time.Hour).UnixMilli(), IsSkip: true},
		{ID: "c", Start: now.UnixMilli(), End: now.Add(time.Hour).UnixMilli(), IsConflict: true},
	}
	if err := storage.WriteJSONAtomic(paths.Reserves, reserves, false); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{RecordedDir: filepath.Join(dir, "recorded"), RecordedFormat: "<id>.m2ts", ConflictedPriority: 7}
	streamer := &priorityStreamer{fakeStreamer: fakeStreamer{body: "x"}}
	result, err := RunOnce(context.Background(), paths, cfg, streamer, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Started != 1 || result.Completed != 1 || result.Failed != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if streamer.priority != 7 {
		t.Fatalf("conflict priority = %d", streamer.priority)
	}
	var recorded []legacy.Program
	if err := storage.ReadJSON(paths.Recorded, &recorded, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || recorded[0].ID != "c" || !recorded[0].IsConflict {
		t.Fatalf("recorded conflict = %#v", recorded)
	}
}

func TestMergeRecordedProgramMatchesLegacyDuplicateHandling(t *testing.T) {
	completed := legacy.Program{ID: "same", Start: 2000, Recorded: "/recorded/new.m2ts"}
	recorded := mergeRecordedProgram([]legacy.Program{
		{ID: "same", Start: 1000, Recorded: "/recorded/new.m2ts"},
		{ID: "other", Recorded: "/recorded/other.m2ts"},
	}, completed)
	if len(recorded) != 2 || recorded[0].ID != "other" || recorded[1].ID != "same" {
		t.Fatalf("same path merge mismatch: %#v", recorded)
	}

	recorded = mergeRecordedProgram([]legacy.Program{
		{ID: "same", Start: 1000, Recorded: "/recorded/old.m2ts"},
		{ID: "other", Recorded: "/recorded/other.m2ts"},
	}, completed)
	if len(recorded) != 3 || recorded[0].ID != "same-rs" || recorded[1].ID != "other" || recorded[2].ID != "same" {
		t.Fatalf("different path merge mismatch: %#v", recorded)
	}
}

func TestRunOnceStopsWhenRecordingAbortIsSet(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	paths := Paths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
		Log:       filepath.Join(dir, "log", "operator"),
	}
	program := legacy.Program{
		ID:      "21i3v9",
		Title:   "Abortable",
		Start:   now.UnixMilli(),
		End:     now.Add(time.Hour).UnixMilli(),
		Channel: legacy.Channel{Type: "GR", Channel: "27", Name: "Service"},
	}
	if err := storage.WriteJSONAtomic(paths.Reserves, []legacy.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{RecordedDir: filepath.Join(dir, "recorded"), RecordedFormat: "<id>.m2ts"}
	streamer := &abortableStreamer{}
	done := make(chan error, 1)
	go func() {
		result, err := RunOnce(context.Background(), paths, cfg, streamer, now)
		if err != nil {
			done <- err
			return
		}
		if result.Started != 1 || result.Completed != 1 || result.Failed != 0 {
			done <- os.ErrInvalid
			return
		}
		done <- nil
	}()

	var recording []legacy.Program
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if err := storage.ReadJSON(paths.Recording, &recording, "[]"); err != nil {
			t.Fatal(err)
		}
		if len(recording) == 1 && recording[0].Recorded != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(recording) != 1 {
		t.Fatalf("recording entry was not created: %#v", recording)
	}
	if recording[0].Recorded == "" || recording[0].PID != -1 {
		t.Fatalf("recording entry missing legacy runtime fields: %#v", recording[0])
	}
	var tuner struct {
		Name         string `json:"name"`
		Command      string `json:"command"`
		IsScrambling bool   `json:"isScrambling"`
	}
	if err := json.Unmarshal(recording[0].Raw["tuner"], &tuner); err != nil {
		t.Fatal(err)
	}
	if tuner.Name != "Mirakurun" || tuner.Command != "*" || tuner.IsScrambling {
		t.Fatalf("unexpected tuner metadata: %#v", tuner)
	}
	var command string
	if err := json.Unmarshal(recording[0].Raw["command"], &command); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, "mirakurun type=GR") || !strings.Contains(command, "priority=2") {
		t.Fatalf("unexpected command metadata: %q", command)
	}
	recording[0].Abort = true
	if err := storage.WriteJSONAtomic(paths.Recording, recording, false); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("recording did not stop after abort flag")
	}
	var recorded []legacy.Program
	if err := storage.ReadJSON(paths.Recorded, &recorded, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || recorded[0].Recorded == "" {
		t.Fatalf("recorded entry missing after abort: %#v", recorded)
	}
}

func TestRunOnceFinalizesActiveRecordingWhenContextIsCancelled(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	paths := Paths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
		Log:       filepath.Join(dir, "log", "operator"),
	}
	program := legacy.Program{
		ID:      "21i3v9",
		Title:   "SignalStop",
		Start:   now.UnixMilli(),
		End:     now.Add(time.Hour).UnixMilli(),
		Channel: legacy.Channel{Type: "GR", Channel: "27", Name: "Service"},
	}
	if err := storage.WriteJSONAtomic(paths.Reserves, []legacy.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		result, err := RunOnce(ctx, paths, &config.Config{
			RecordedDir:    filepath.Join(dir, "recorded"),
			RecordedFormat: "<id>.m2ts",
		}, &abortableStreamer{}, now)
		if err != nil {
			done <- err
			return
		}
		if result.Started != 1 || result.Completed != 1 || result.Failed != 0 {
			done <- os.ErrInvalid
			return
		}
		done <- nil
	}()

	var recording []legacy.Program
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if err := storage.ReadJSON(paths.Recording, &recording, "[]"); err != nil {
			t.Fatal(err)
		}
		if len(recording) == 1 && recording[0].Recorded != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(recording) != 1 || recording[0].Recorded == "" {
		t.Fatalf("recording entry was not active before cancel: %#v", recording)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("recording did not stop after context cancellation")
	}
	if err := storage.ReadJSON(paths.Recording, &recording, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(recording) != 0 {
		t.Fatalf("recording state was not cleared after cancel: %#v", recording)
	}
	var recorded []legacy.Program
	if err := storage.ReadJSON(paths.Recorded, &recorded, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || recorded[0].Recorded == "" {
		t.Fatalf("recorded entry missing after cancel: %#v", recorded)
	}
	logData, err := os.ReadFile(paths.Log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FIN: 21i3v9",
		"FIN: #21i3v9 ",
	} {
		if !strings.Contains(string(logData), want) {
			t.Fatalf("operator log missing %q after cancel: %s", want, string(logData))
		}
	}
	assertLogSubsequence(t, logData,
		"PREPARE: #21i3v9 ",
		"WRITE: "+paths.Recording,
		"START: 21i3v9",
		"RECORD: #21i3v9 ",
		"STREAM: ",
		"WRITE: "+paths.Recording,
		"WRITE: "+paths.Recording,
		"WRITE: "+paths.Recorded,
		"FIN: 21i3v9",
		"FIN: #21i3v9 ",
	)
}

func TestPIDFileLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "operator.pid")
	if err := writePIDFile(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := strconv.Itoa(os.Getpid()) + "\n"
	if string(data) != want {
		t.Fatalf("pid file = %q, want %q", data, want)
	}
	removePIDFile(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("pid file was not removed: %v", err)
	}
}

func TestRunOnceLowStorageRemoveDeletesOldestRecorded(t *testing.T) {
	dir := t.TempDir()
	oldGetDiskUsage := getDiskUsage
	getDiskUsage = func(string) (system.DiskUsage, error) {
		return system.DiskUsage{Avail: 10 * 1024 * 1024}, nil
	}
	defer func() { getDiskUsage = oldGetDiskUsage }()

	oldFile := filepath.Join(dir, "recorded", "old.m2ts")
	if err := os.MkdirAll(filepath.Dir(oldFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := Paths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
		Log:       filepath.Join(dir, "log", "operator"),
	}
	if err := storage.WriteJSONAtomic(paths.Reserves, []legacy.Program{}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recording, []legacy.Program{}, false); err != nil {
		t.Fatal(err)
	}
	recorded := []legacy.Program{{ID: "old", Recorded: filepath.ToSlash(oldFile)}, {ID: "new", Recorded: filepath.ToSlash(filepath.Join(dir, "recorded", "new.m2ts"))}}
	if err := storage.WriteJSONAtomic(paths.Recorded, recorded, false); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		RecordedDir:                filepath.Join(dir, "recorded"),
		StorageLowSpaceThresholdMB: 100,
		StorageLowSpaceAction:      "remove",
	}
	result, err := RunOnce(context.Background(), paths, cfg, &fakeStreamer{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result != (Result{}) {
		t.Fatalf("unexpected result: %#v", result)
	}
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Fatalf("old recorded file was not removed: %v", err)
	}
	var remaining []legacy.Program
	if err := storage.ReadJSON(paths.Recorded, &remaining, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != "new" {
		t.Fatalf("unexpected recorded list: %#v", remaining)
	}
	backups, err := filepath.Glob(paths.Recorded + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("backup count = %d backups=%#v", len(backups), backups)
	}
	var backup []legacy.Program
	if err := storage.ReadJSON(backups[0], &backup, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(backup) != 2 {
		t.Fatalf("backup should contain original recorded list: %#v", backup)
	}
}

func TestLowStorageStopMarksActiveRecordingsAbort(t *testing.T) {
	dir := t.TempDir()
	oldGetDiskUsage := getDiskUsage
	getDiskUsage = func(string) (system.DiskUsage, error) {
		return system.DiskUsage{Avail: 10 * 1024 * 1024}, nil
	}
	defer func() { getDiskUsage = oldGetDiskUsage }()

	paths := Paths{
		Recording: filepath.Join(dir, "data", "recording.json"),
		Log:       filepath.Join(dir, "log", "operator"),
	}
	recording := []legacy.Program{
		{ID: "active", Title: "Active", Channel: legacy.Channel{Name: "Service"}},
		{ID: "already", Title: "Already", Abort: true, Channel: legacy.Channel{Name: "Service"}},
	}
	cfg := &config.Config{
		RecordedDir:                filepath.Join(dir, "recorded"),
		StorageLowSpaceThresholdMB: 100,
		StorageLowSpaceAction:      "stop",
	}

	if _, err := handleLowStorage(context.Background(), paths, cfg, recording, nil); err != nil {
		t.Fatal(err)
	}
	var updated []legacy.Program
	if err := storage.ReadJSON(paths.Recording, &updated, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(updated) != 2 || !updated[0].Abort || !updated[1].Abort {
		t.Fatalf("recordings were not marked abort: %#v", updated)
	}
	logData, err := os.ReadFile(paths.Log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "WRITE: "+paths.Recording) {
		t.Fatalf("operator log missing recording write: %s", string(logData))
	}
}
