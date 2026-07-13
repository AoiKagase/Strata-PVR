package operator

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"strata-pvr/internal/config"
	legacy "strata-pvr/internal/domain"
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

type testPaths struct {
	Config    string
	Database  string
	Reserves  string
	Recording string
	Recorded  string
	PID       string
	Log       string
}

func (p testPaths) runtime() Paths {
	return Paths{Config: p.Config, Database: p.Database, PID: p.PID, Log: p.Log}
}

func (p testPaths) writeLog(collection string) string {
	databasePath := p.Database
	if databasePath == "" {
		databasePath = filepath.Join(filepath.Dir(p.Recording), "strata.db")
	}
	return "WRITE: " + databasePath + " (" + collection + ")"
}

func runOnceTest(t *testing.T, ctx context.Context, paths testPaths, cfg *config.Config, source StreamSource, now time.Time) (Result, error) {
	t.Helper()
	legacyFixture := paths.Database == ""
	paths = seedOperatorTestDatabase(t, paths)
	result, err := RunOnce(ctx, paths.runtime(), cfg, source, now)
	if legacyFixture {
		exportOperatorTestState(paths)
	}
	return result, err
}

func initializeRuntimeStateTest(t *testing.T, paths testPaths, cfg *config.Config) error {
	t.Helper()
	legacyFixture := paths.Database == ""
	paths = seedOperatorTestDatabase(t, paths)
	err := initializeRuntimeState(paths.runtime(), cfg)
	if legacyFixture {
		exportOperatorTestState(paths)
	}
	return err
}

func seedOperatorTestDatabase(t *testing.T, paths testPaths) testPaths {
	t.Helper()
	if paths.Database != "" {
		return paths
	}
	paths.Database = filepath.Join(filepath.Dir(paths.Recording), "strata.db")
	ctx := context.Background()
	var reserves []legacy.Program
	if storage.ReadJSON(paths.Reserves, &reserves, "[]") == nil {
		if err := reservationstore.Write(ctx, paths.Database, reserves); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct{ path, collection string }{{paths.Recording, programstore.Recording}, {paths.Recorded, programstore.Recorded}} {
		var programs []legacy.Program
		if storage.ReadJSON(item.path, &programs, "[]") == nil {
			if err := programstore.Write(ctx, paths.Database, item.collection, programs); err != nil {
				t.Fatal(err)
			}
		}
	}
	return paths
}

func exportOperatorTestState(paths testPaths) {
	ctx := context.Background()
	if reserves, err := reservationstore.Read(ctx, paths.Database); err == nil {
		_ = storage.WriteJSONAtomic(paths.Reserves, reserves, false)
	}
	for _, item := range []struct{ path, collection string }{{paths.Recording, programstore.Recording}, {paths.Recorded, programstore.Recorded}} {
		if programs, err := programstore.Read(ctx, paths.Database, item.collection); err == nil {
			_ = storage.WriteJSONAtomic(item.path, programs, false)
		}
	}
}

func readOperatorTestPrograms(paths testPaths, collection string, target *[]legacy.Program) error {
	databasePath := paths.Database
	if databasePath == "" {
		databasePath = filepath.Join(filepath.Dir(paths.Recording), "strata.db")
	}
	programs, err := programstore.Read(context.Background(), databasePath, collection)
	if err == nil {
		*target = programs
	}
	return err
}

func readOperatorTestReserves(paths testPaths, target *[]legacy.Program) error {
	databasePath := paths.Database
	if databasePath == "" {
		databasePath = filepath.Join(filepath.Dir(paths.Recording), "strata.db")
	}
	reserves, err := reservationstore.Read(context.Background(), databasePath)
	if err == nil {
		*target = reserves
	}
	return err
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

type concurrentStreamer struct {
	started chan int64
	release <-chan struct{}
}

func (s *concurrentStreamer) ProgramStream(_ context.Context, id int64, _ bool) (io.ReadCloser, error) {
	reader, writer := io.Pipe()
	s.started <- id
	go func() {
		<-s.release
		_, _ = writer.Write([]byte("ts"))
		_ = writer.Close()
	}()
	return reader, nil
}

func (f *priorityStreamer) SetPriority(priority int) {
	f.priority = priority
}

func TestStartPendingRecordingsStartsOverlappingReservations(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths := seedOperatorTestDatabase(t, testPaths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
		Log:       filepath.Join(dir, "log", "operator"),
	})
	reserves := []legacy.Program{
		{ID: "a", Start: now.UnixMilli(), End: now.Add(time.Hour).UnixMilli(), Channel: legacy.Channel{Type: "GR"}},
		{ID: "b", Start: now.UnixMilli(), End: now.Add(time.Hour).UnixMilli(), Channel: legacy.Channel{Type: "GR"}},
	}
	if err := reservationstore.Write(context.Background(), paths.Database, reserves); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	streamer := &concurrentStreamer{started: make(chan int64, 2), release: release}
	var recordings sync.WaitGroup
	if err := startPendingRecordings(context.Background(), paths.runtime(), &config.Config{RecordedDir: filepath.Join(dir, "recorded"), RecordedFormat: "<id>.m2ts"}, streamer, now, &recordings); err != nil {
		t.Fatal(err)
	}
	for range reserves {
		select {
		case <-streamer.started:
		case <-time.After(time.Second):
			t.Fatal("overlapping recording did not start")
		}
	}
	close(release)
	recordings.Wait()
}

func TestAbortSkippedRecordingsStopsOnlySkippedAutoReserves(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths := seedOperatorTestDatabase(t, testPaths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
	})
	reserves := []legacy.Program{{ID: "skip", IsSkip: true}, {ID: "keep"}, {ID: "manual", IsSkip: true, IsManualReserved: true}}
	recording := []legacy.Program{{ID: "skip"}, {ID: "keep"}, {ID: "manual", IsManualReserved: true}}
	if err := programstore.Write(context.Background(), paths.Database, programstore.Recording, recording); err != nil {
		t.Fatal(err)
	}
	updated, err := abortSkippedRecordings(context.Background(), paths.Database, reserves, recording)
	if err != nil {
		t.Fatal(err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d", updated)
	}
	if err := readOperatorTestPrograms(paths, programstore.Recording, &recording); err != nil {
		t.Fatal(err)
	}
	if !recording[0].Abort || recording[1].Abort || recording[2].Abort {
		t.Fatalf("recording abort states = %#v", recording)
	}
}

func TestInitializeRuntimeStateClearsRecordingAndCreatesRecordedDir(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths{
		Recording: filepath.Join(dir, "data", "recording.json"),
		Log:       filepath.Join(dir, "log", "operator"),
	}
	if err := storage.WriteJSONAtomic(paths.Recording, []legacy.Program{{ID: "stale"}}, false); err != nil {
		t.Fatal(err)
	}
	recordedDir := filepath.Join(dir, "recorded")
	if err := initializeRuntimeStateTest(t, paths, &config.Config{RecordedDir: recordedDir}); err != nil {
		t.Fatal(err)
	}
	var recording []legacy.Program
	if err := readOperatorTestPrograms(paths, programstore.Recording, &recording); err != nil {
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
	paths := testPaths{
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
	result, err := runOnceTest(t, context.Background(), paths, cfg, streamer, now)
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
	if err := readOperatorTestReserves(paths, &reserves); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 0 {
		t.Fatalf("completed auto reserve should be removed: %#v", reserves)
	}
	var recording []legacy.Program
	if err := readOperatorTestPrograms(paths, programstore.Recording, &recording); err != nil {
		t.Fatal(err)
	}
	if len(recording) != 0 {
		t.Fatalf("recording not cleared: %#v", recording)
	}
	var recorded []legacy.Program
	if err := readOperatorTestPrograms(paths, programstore.Recorded, &recorded); err != nil {
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
		paths.writeLog(programstore.Recording),
		paths.writeLog(programstore.Recorded),
		"FIN: #21i3v9 ",
	} {
		if !strings.Contains(string(logData), want) {
			t.Fatalf("operator log missing %q: %s", want, string(logData))
		}
	}
	if got := strings.Count(string(logData), paths.writeLog(programstore.Recording)); got < 2 {
		t.Fatalf("operator log should include initial and completion recording writes, got %d: %s", got, string(logData))
	}
	assertLogSubsequence(t, logData,
		"PREPARE: #21i3v9 ",
		paths.writeLog(programstore.Recording),
		"START: 21i3v9",
		"RECORD: #21i3v9 ",
		"STREAM: ",
		paths.writeLog(programstore.Recording),
		paths.writeLog(programstore.Recording),
		paths.writeLog(programstore.Recorded),
		"FIN: 21i3v9",
		"FIN: #21i3v9 ",
	)
}

func TestRunOnceMovesProgramToRecordedInStrataDatabase(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	paths := testPaths{
		Database: filepath.Join(dir, "data", "strata.db"), Reserves: filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"), Recorded: filepath.Join(dir, "data", "recorded.json"), Log: filepath.Join(dir, "log", "operator"),
	}
	if err := os.MkdirAll(filepath.Dir(paths.Database), 0o755); err != nil {
		t.Fatal(err)
	}
	program := legacy.Program{ID: "21i3v9", Title: "Database", Start: now.UnixMilli(), End: now.Add(time.Hour).UnixMilli(), Channel: legacy.Channel{Type: "GR", Channel: "27"}, IsManualReserved: true}
	if err := reservationstore.Upsert(context.Background(), paths.Database, program); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{RecordedDir: filepath.Join(dir, "recorded"), RecordedFormat: "<id>.m2ts"}
	result, err := runOnceTest(t, context.Background(), paths, cfg, &fakeStreamer{body: "database-ts"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Completed != 1 {
		t.Fatalf("result = %#v", result)
	}
	recording, err := programstore.Read(context.Background(), paths.Database, programstore.Recording)
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := programstore.Read(context.Background(), paths.Database, programstore.Recorded)
	if err != nil {
		t.Fatal(err)
	}
	reserves, err := reservationstore.Read(context.Background(), paths.Database)
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
	paths := testPaths{
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
	paths = seedOperatorTestDatabase(t, paths)
	cfg := &config.Config{RecordedDir: filepath.Join(dir, "recorded"), RecordedFormat: "<id>.m2ts"}
	result, err := runOnceTest(t, context.Background(), paths, cfg, &fakeStreamer{body: "tsdata"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Completed != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	var reserves []legacy.Program
	if err := readOperatorTestReserves(paths, &reserves); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 0 {
		t.Fatalf("manual reserve was not removed: %#v", reserves)
	}
	logData, err := os.ReadFile(paths.Log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), paths.writeLog("reserves")) {
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
	if _, err := recordProgram(context.Background(), cfg, normal, program); err != nil {
		t.Fatal(err)
	}
	if normal.priority != 5 {
		t.Fatalf("normal priority = %d", normal.priority)
	}

	conflicted := &priorityStreamer{fakeStreamer: fakeStreamer{body: "conflict"}}
	program.IsConflict = true
	if _, err := recordProgram(context.Background(), cfg, conflicted, program); err != nil {
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
	paths := testPaths{
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
	result, err := runOnceTest(t, context.Background(), paths, cfg, streamer, now)
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
	if err := readOperatorTestPrograms(paths, programstore.Recorded, &recorded); err != nil {
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
	paths := testPaths{
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
	paths = seedOperatorTestDatabase(t, paths)
	cfg := &config.Config{RecordedDir: filepath.Join(dir, "recorded"), RecordedFormat: "<id>.m2ts"}
	streamer := &abortableStreamer{}
	done := make(chan error, 1)
	go func() {
		result, err := runOnceTest(t, context.Background(), paths, cfg, streamer, now)
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

	for deadline := time.Now().Add(2 * time.Second); streamer.stream == nil && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if streamer.stream == nil {
		t.Fatal("recording stream was not started")
	}
	time.Sleep(200 * time.Millisecond)
	program.Abort = true
	if err := programstore.Upsert(context.Background(), paths.Database, programstore.Recording, program); err != nil {
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
	if err := readOperatorTestPrograms(paths, programstore.Recorded, &recorded); err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || recorded[0].Recorded == "" {
		t.Fatalf("recorded entry missing after abort: %#v", recorded)
	}
}

func TestRunOnceFinalizesActiveRecordingWhenContextIsCancelled(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	paths := testPaths{
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
	paths = seedOperatorTestDatabase(t, paths)
	ctx, cancel := context.WithCancel(context.Background())
	streamer := &abortableStreamer{}
	done := make(chan error, 1)
	go func() {
		result, err := runOnceTest(t, ctx, paths, &config.Config{
			RecordedDir:    filepath.Join(dir, "recorded"),
			RecordedFormat: "<id>.m2ts",
		}, streamer, now)
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
		if err := readOperatorTestPrograms(paths, programstore.Recording, &recording); err == nil && len(recording) == 1 && recording[0].Recorded != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
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
	if err := readOperatorTestPrograms(paths, programstore.Recording, &recording); err != nil {
		t.Fatal(err)
	}
	if len(recording) != 0 {
		t.Fatalf("recording state was not cleared after cancel: %#v", recording)
	}
	var recorded []legacy.Program
	if err := readOperatorTestPrograms(paths, programstore.Recorded, &recorded); err != nil {
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
		paths.writeLog(programstore.Recording),
		"START: 21i3v9",
		"RECORD: #21i3v9 ",
		"STREAM: ",
		paths.writeLog(programstore.Recording),
		paths.writeLog(programstore.Recording),
		paths.writeLog(programstore.Recorded),
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

func TestRemovePIDFileKeepsAnotherProcessPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "operator.pid")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	removePIDFile(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "999999\n"; got != want {
		t.Fatalf("pid file = %q, want %q", got, want)
	}
}

func TestAcquireProcessLockUsesPIDLockPath(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "data", "operator.pid")
	first, err := acquireProcessLock(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := acquireProcessLock(pidPath); !errors.Is(err, system.ErrProcessAlreadyRunning) {
		t.Fatalf("second operator lock error = %v", err)
	}
}

func TestAcquireProcessLockAllowsEmptyPIDPath(t *testing.T) {
	lock, err := acquireProcessLock("")
	if err != nil {
		t.Fatal(err)
	}
	if lock != nil {
		defer lock.Close()
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
	paths := testPaths{
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
	result, err := runOnceTest(t, context.Background(), paths, cfg, &fakeStreamer{}, time.Now())
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
	if err := readOperatorTestPrograms(paths, programstore.Recorded, &remaining); err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != "new" {
		t.Fatalf("unexpected recorded list: %#v", remaining)
	}
}

func TestLowStorageStopMarksActiveRecordingsAbort(t *testing.T) {
	dir := t.TempDir()
	oldGetDiskUsage := getDiskUsage
	getDiskUsage = func(string) (system.DiskUsage, error) {
		return system.DiskUsage{Avail: 10 * 1024 * 1024}, nil
	}
	defer func() { getDiskUsage = oldGetDiskUsage }()

	paths := testPaths{
		Database:  filepath.Join(dir, "data", "strata.db"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Log:       filepath.Join(dir, "log", "operator"),
	}
	recording := []legacy.Program{
		{ID: "active", Title: "Active", Channel: legacy.Channel{Name: "Service"}},
		{ID: "already", Title: "Already", Abort: true, Channel: legacy.Channel{Name: "Service"}},
	}
	if err := os.MkdirAll(filepath.Dir(paths.Database), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := programstore.Write(context.Background(), paths.Database, programstore.Recording, recording); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		RecordedDir:                filepath.Join(dir, "recorded"),
		StorageLowSpaceThresholdMB: 100,
		StorageLowSpaceAction:      "stop",
	}

	if _, err := handleLowStorage(context.Background(), paths.runtime(), cfg, recording, nil); err != nil {
		t.Fatal(err)
	}
	var updated []legacy.Program
	if err := readOperatorTestPrograms(paths, programstore.Recording, &updated); err != nil {
		t.Fatal(err)
	}
	if len(updated) != 2 || !updated[0].Abort || !updated[1].Abort {
		t.Fatalf("recordings were not marked abort: %#v", updated)
	}
	logData, err := os.ReadFile(paths.Log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), paths.writeLog(programstore.Recording)) {
		t.Fatalf("operator log missing recording write: %s", string(logData))
	}
}
