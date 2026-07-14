package operator

import (
	"context"
	"encoding/json"
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
	"strata-pvr/internal/database"
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

type endMarginStreamer struct {
	closed chan struct{}
	once   sync.Once
}

func (s *endMarginStreamer) ProgramStream(context.Context, int64, bool) (io.ReadCloser, error) {
	return s, nil
}

func (s *endMarginStreamer) Read([]byte) (int, error) {
	<-s.closed
	return 0, io.EOF
}

func (s *endMarginStreamer) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

type abortAtEOFStreamer struct {
	databasePath string
	programID    string
}

func (s *abortAtEOFStreamer) ProgramStream(context.Context, int64, bool) (io.ReadCloser, error) {
	return &abortAtEOFReader{reader: strings.NewReader("partial"), databasePath: s.databasePath, programID: s.programID}, nil
}

type abortAtEOFReader struct {
	reader       *strings.Reader
	databasePath string
	programID    string
	aborted      bool
}

func (r *abortAtEOFReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err == io.EOF && !r.aborted {
		r.aborted = true
		_ = programstore.SetAbort(context.Background(), r.databasePath, programstore.Recording, r.programID, true)
	}
	return n, err
}

func (r *abortAtEOFReader) Close() error { return nil }

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

func TestShouldStartWithConfiguredMargin(t *testing.T) {
	now := time.Unix(1000, 0)
	program := legacy.Program{ID: "margin", Start: now.Add(8 * time.Second).UnixMilli(), End: now.Add(time.Hour).UnixMilli()}
	if !shouldStartWithMargin(program, nil, now, 10*time.Second) {
		t.Fatal("program was not due within the configured start margin")
	}
	if shouldStartWithMargin(program, nil, now, 5*time.Second) {
		t.Fatal("program started before the configured start margin")
	}
	if shouldStartWithMargin(program, nil, now, -10*time.Second) {
		t.Fatal("program started before a negative configured start margin")
	}
	if !shouldStartWithMargin(program, nil, now.Add(19*time.Second), -10*time.Second) {
		t.Fatal("program did not start after a negative configured start margin")
	}
}

func TestRecordProgramHonorsConfiguredEndMargin(t *testing.T) {
	dir := t.TempDir()
	streamer := &endMarginStreamer{closed: make(chan struct{})}
	cfg := &config.Config{
		RecordedDir:        dir,
		RecordedFormat:     "<id>.m2ts",
		RecordingEndMargin: 1,
	}
	program := legacy.Program{ID: "1", Start: time.Now().UnixMilli(), End: time.Now().Add(100 * time.Millisecond).UnixMilli()}
	started := time.Now()
	if _, err := recordProgram(context.Background(), cfg, streamer, program); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 900*time.Millisecond {
		t.Fatalf("recording ended after %s; configured end margin was not applied", elapsed)
	}
}

func TestRecordProgramHonorsNegativeEndMargin(t *testing.T) {
	dir := t.TempDir()
	streamer := &endMarginStreamer{closed: make(chan struct{})}
	cfg := &config.Config{
		RecordedDir:        dir,
		RecordedFormat:     "<id>.m2ts",
		RecordingEndMargin: -1,
	}
	program := legacy.Program{ID: "1", Start: time.Now().UnixMilli(), End: time.Now().Add(1500 * time.Millisecond).UnixMilli()}
	started := time.Now()
	if _, err := recordProgram(context.Background(), cfg, streamer, program); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 250*time.Millisecond || elapsed > 1*time.Second {
		t.Fatalf("recording ended after %s; negative configured end margin was not applied", elapsed)
	}
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
	updated, err := abortSkippedRecordings(context.Background(), paths.Database, reserves, []string{"skip", "keep", "manual"})
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

func TestAbortSkippedRecordingsDoesNotStopManualOrUnrelatedRecording(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths := seedOperatorTestDatabase(t, testPaths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
	})
	reserves := []legacy.Program{
		{ID: "skip", IsSkip: true},
		{ID: "manual", IsSkip: true, IsManualReserved: true},
	}
	recording := []legacy.Program{
		{ID: "skip"},
		{ID: "manual", IsManualReserved: true},
		{ID: "other"},
	}
	if err := programstore.Write(context.Background(), paths.Database, programstore.Recording, recording); err != nil {
		t.Fatal(err)
	}
	updated, err := abortSkippedRecordings(context.Background(), paths.Database, reserves, []string{"skip", "manual", "other"})
	if err != nil || updated != 1 {
		t.Fatalf("updated=%d err=%v", updated, err)
	}
	if err := readOperatorTestPrograms(paths, programstore.Recording, &recording); err != nil {
		t.Fatal(err)
	}
	if !recording[0].Abort || recording[1].Abort || recording[2].Abort {
		t.Fatalf("recording abort states = %#v", recording)
	}
}

func TestAbortSkippedRecordingsIgnoresCompletionInProgress(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths := seedOperatorTestDatabase(t, testPaths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
	})
	reserves := []legacy.Program{{ID: "finalizing", IsSkip: true}}
	recording := []legacy.Program{{
		ID:  "finalizing",
		Raw: map[string]json.RawMessage{"_strataFinalizing": json.RawMessage(`true`)},
	}}
	if err := programstore.Write(context.Background(), paths.Database, programstore.Recording, recording); err != nil {
		t.Fatal(err)
	}

	updated, err := abortSkippedRecordings(context.Background(), paths.Database, reserves, []string{"finalizing"})
	if err != nil {
		t.Fatal(err)
	}
	if updated != 0 {
		t.Fatalf("updated = %d, want 0", updated)
	}
	if err := readOperatorTestPrograms(paths, programstore.Recording, &recording); err != nil {
		t.Fatal(err)
	}
	if len(recording) != 1 || recording[0].Abort {
		t.Fatalf("finalizing recording was aborted: %#v", recording)
	}
}

func TestStartPendingRecordingsAbortsLateSkippedActiveRecording(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	paths := testPaths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
		Log:       filepath.Join(dir, "log", "operator"),
	}
	if err := os.MkdirAll(filepath.Dir(paths.Recording), 0o755); err != nil {
		t.Fatal(err)
	}
	program := legacy.Program{ID: "late", Start: 0, End: now.Add(-time.Minute).UnixMilli(), IsSkip: true}
	if err := storage.WriteJSONAtomic(paths.Reserves, []legacy.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recording, []legacy.Program{{ID: program.ID}}, false); err != nil {
		t.Fatal(err)
	}
	paths = seedOperatorTestDatabase(t, paths)
	var recordings sync.WaitGroup
	if err := startPendingRecordings(context.Background(), paths.runtime(), &config.Config{}, &fakeStreamer{}, now, &recordings); err != nil {
		t.Fatal(err)
	}
	recordings.Wait()
	updated, found, err := programstore.ReadByID(context.Background(), paths.Database, programstore.Recording, program.ID)
	if err != nil || !found {
		t.Fatal(err)
	}
	if !updated.Abort {
		t.Fatalf("late skipped active recording was not aborted: %#v", updated)
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

func TestInitializeRuntimeStateRemovesStaleActiveRecordingFiles(t *testing.T) {
	dir := t.TempDir()
	finalPath := filepath.Join(dir, "recorded", "stale.m2ts")
	partPath := finalPath + ".part"
	paths := testPaths{
		Recording: filepath.Join(dir, "data", "recording.json"),
		Log:       filepath.Join(dir, "log", "operator"),
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{finalPath, partPath} {
		if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := storage.WriteJSONAtomic(paths.Recording, []legacy.Program{{
		ID:       "stale",
		Recorded: filepath.ToSlash(finalPath),
	}}, false); err != nil {
		t.Fatal(err)
	}

	if err := initializeRuntimeStateTest(t, paths, &config.Config{RecordedDir: filepath.Join(dir, "recorded")}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{finalPath, partPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("stale active recording file %s still exists: %v", path, err)
		}
	}
	var recording []legacy.Program
	if err := readOperatorTestPrograms(paths, programstore.Recording, &recording); err != nil {
		t.Fatal(err)
	}
	if len(recording) != 0 {
		t.Fatalf("recording state was not cleared: %#v", recording)
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
	type runOutcome struct {
		result Result
		err    error
	}
	done := make(chan runOutcome, 1)
	go func() {
		result, err := runOnceTest(t, context.Background(), paths, cfg, streamer, now)
		done <- runOutcome{result: result, err: err}
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
	case outcome := <-done:
		if !errors.Is(outcome.err, context.Canceled) {
			t.Fatalf("abort error = %v", outcome.err)
		}
		if outcome.result.Started != 1 || outcome.result.Completed != 0 || outcome.result.Failed != 1 {
			t.Fatalf("unexpected abort result: %#v", outcome.result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("recording did not stop after abort flag")
	}
	var recorded []legacy.Program
	if err := readOperatorTestPrograms(paths, programstore.Recorded, &recorded); err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 0 {
		t.Fatalf("aborted recording was promoted: %#v", recorded)
	}
	var reserves []legacy.Program
	if err := readOperatorTestReserves(paths, &reserves); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 1 || reserves[0].ID != program.ID {
		t.Fatalf("reservation was removed after abort: %#v", reserves)
	}
}

func TestRecordProgramRejectsAbortSetAtEOFBeforePromotion(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
		Log:       filepath.Join(dir, "log", "operator"),
	}
	if err := os.MkdirAll(filepath.Dir(paths.Recording), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.Reserves, paths.Recording, paths.Recorded} {
		if err := storage.WriteJSONAtomic(path, []legacy.Program{}, false); err != nil {
			t.Fatal(err)
		}
	}
	paths = seedOperatorTestDatabase(t, paths)
	program := legacy.Program{ID: "eofabort", Start: 1000, End: 3600000, Channel: legacy.Channel{Type: "GR", Channel: "27"}}
	if err := programstore.Write(context.Background(), paths.Database, programstore.Recording, []legacy.Program{program}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{RecordedDir: filepath.Join(dir, "recorded"), RecordedFormat: "<id>.m2ts"}
	completed, err := recordProgramWithLog(context.Background(), paths.Database, paths.Log, cfg, &abortAtEOFStreamer{databasePath: paths.Database, programID: program.ID}, program)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("recording error = %v, want context.Canceled (completed=%#v)", err, completed)
	}
	finalPath := filepath.Join(cfg.RecordedDir, "eofabort.m2ts")
	for _, path := range []string{finalPath, finalPath + ".part"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("aborted output %s exists: %v", path, err)
		}
	}
}

func TestRecordProgramPromotionFailureRemovesClaimedActiveRow(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths{
		Database: filepath.Join(dir, "data", "strata.db"),
		Log:      filepath.Join(dir, "log", "operator"),
	}
	if err := os.MkdirAll(filepath.Dir(paths.Database), 0o755); err != nil {
		t.Fatal(err)
	}
	program := legacy.Program{ID: "promfail", Start: time.Now().Add(-time.Minute).UnixMilli(), End: time.Now().Add(time.Hour).UnixMilli()}
	if err := programstore.Upsert(context.Background(), paths.Database, programstore.Recording, program); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{RecordedDir: filepath.Join(dir, "recorded"), RecordedFormat: "<id>.m2ts"}
	finalPath := filepath.Join(cfg.RecordedDir, "promfail.m2ts")
	if err := os.MkdirAll(finalPath, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := recordProgramWithLog(context.Background(), paths.Database, paths.Log, cfg, &fakeStreamer{body: "ts"}, program)
	if err == nil {
		t.Fatal("promotion unexpectedly succeeded")
	}
	active, err := programstore.Read(context.Background(), paths.Database, programstore.Recording)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("claimed active row was left after promotion failure: %#v", active)
	}
	if info, err := os.Stat(finalPath); err != nil || !info.IsDir() {
		t.Fatalf("existing final path was not preserved: info=%v err=%v", info, err)
	}
}

func TestRunOnceCancellingContextLeavesReservationAndNoRecordedOutput(t *testing.T) {
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
	type runOnceOutcome struct {
		result Result
		err    error
	}
	done := make(chan runOnceOutcome, 1)
	go func() {
		result, err := RunOnce(ctx, paths.runtime(), &config.Config{
			RecordedDir:    filepath.Join(dir, "recorded"),
			RecordedFormat: "<id>.m2ts",
		}, streamer, now)
		done <- runOnceOutcome{result: result, err: err}
	}()

	for deadline := time.Now().Add(2 * time.Second); streamer.stream == nil && time.Now().Before(deadline); {
		select {
		case outcome := <-done:
			if outcome.err != nil {
				t.Fatal(outcome.err)
			}
			t.Fatalf("RunOnce returned before recording started: %#v", outcome.result)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if streamer.stream == nil {
		t.Fatal("recording stream was not started")
	}

	var recording []legacy.Program
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		select {
		case outcome := <-done:
			if outcome.err != nil {
				t.Fatal(outcome.err)
			}
			t.Fatalf("RunOnce returned before recording became active: %#v", outcome.result)
		default:
		}
		if err := readOperatorTestPrograms(paths, programstore.Recording, &recording); err == nil && len(recording) == 1 && recording[0].Recorded != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := readOperatorTestPrograms(paths, programstore.Recording, &recording); err != nil {
		t.Fatal(err)
	}
	if len(recording) != 1 || recording[0].Recorded == "" {
		t.Fatalf("recording entry was not active before cancel: %#v", recording)
	}
	cancel()
	select {
	case outcome := <-done:
		if !errors.Is(outcome.err, context.Canceled) {
			t.Fatalf("RunOnce cancellation error = %v", outcome.err)
		}
		if outcome.result.Started != 1 || outcome.result.Completed != 0 || outcome.result.Failed != 1 {
			t.Fatalf("unexpected result: %#v", outcome.result)
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
	if len(recorded) != 0 {
		t.Fatalf("cancelled recording was promoted to recorded: %#v", recorded)
	}
	var remainingReserves []legacy.Program
	if err := readOperatorTestReserves(paths, &remainingReserves); err != nil {
		t.Fatal(err)
	}
	if len(remainingReserves) != 1 || remainingReserves[0].ID != program.ID {
		t.Fatalf("reservation was not retained after cancel: %#v", remainingReserves)
	}
	finalPath := filepath.Join(filepath.Join(dir, "recorded"), "21i3v9.m2ts")
	for _, path := range []string{finalPath, finalPath + ".part"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("cancelled recording output %s still exists: %v", path, err)
		}
	}
}

func TestRunOnceDoesNotRerecordReservationAlreadyInRecorded(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths{Database: filepath.Join(dir, "data", "strata.db")}
	if err := os.MkdirAll(filepath.Dir(paths.Database), 0o755); err != nil {
		t.Fatal(err)
	}
	program := legacy.Program{
		ID:    "rec1",
		Start: time.Now().Add(-time.Minute).UnixMilli(),
		End:   time.Now().Add(time.Hour).UnixMilli(),
	}
	if err := reservationstore.Upsert(context.Background(), paths.Database, program); err != nil {
		t.Fatal(err)
	}
	if err := programstore.Upsert(context.Background(), paths.Database, programstore.Recorded, program); err != nil {
		t.Fatal(err)
	}
	streamer := &fakeStreamer{}
	result, err := RunOnce(context.Background(), paths.runtime(), &config.Config{RecordedDir: filepath.Join(dir, "recorded"), RecordedFormat: "<id>.m2ts"}, streamer, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result != (Result{}) {
		t.Fatalf("already-recorded reservation was started: %#v", result)
	}
	if streamer.id != 0 {
		t.Fatalf("stream was opened for already-recorded program: %d", streamer.id)
	}
}

func TestRunOnceRetriesReservationDeleteAfterDatabaseRecovers(t *testing.T) {
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "data", "strata.db")
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	program := legacy.Program{ID: "delretry", Start: now.UnixMilli(), End: now.Add(time.Hour).UnixMilli()}
	if err := reservationstore.Upsert(context.Background(), databasePath, program); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(context.Background(), `CREATE TRIGGER fail_reservation_delete
		BEFORE DELETE ON reservations
		BEGIN SELECT RAISE(ABORT, 'injected reservation delete failure'); END;`)
	if closeErr := db.Close(); err != nil {
		t.Fatal(err)
	} else if closeErr != nil {
		t.Fatal(closeErr)
	}
	paths := testPaths{Database: databasePath}
	cfg := &config.Config{RecordedDir: filepath.Join(dir, "recorded"), RecordedFormat: "<id>.m2ts"}
	if _, err := RunOnce(context.Background(), paths.runtime(), cfg, &fakeStreamer{body: "ts"}, now); err == nil {
		t.Fatal("RunOnce succeeded despite reservation delete failure")
	}
	recorded, err := programstore.Read(context.Background(), databasePath, programstore.Recorded)
	if err != nil || len(recorded) != 1 {
		t.Fatalf("recorded state after delete failure = %#v err=%v", recorded, err)
	}

	db, err = database.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), "DROP TRIGGER fail_reservation_delete"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := RunOnce(context.Background(), paths.runtime(), cfg, &fakeStreamer{}, now); err != nil {
		t.Fatal(err)
	}
	reserves, err := reservationstore.Read(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 0 {
		t.Fatalf("reservation delete was not retried: %#v", reserves)
	}
}

func TestRunOnceRetryReplacesStaleOutputInsteadOfAppending(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	paths := testPaths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
		Log:       filepath.Join(dir, "log", "operator"),
	}
	program := legacy.Program{ID: "retry", Start: now.UnixMilli(), End: now.Add(time.Hour).UnixMilli()}
	cfg := &config.Config{RecordedDir: filepath.Join(dir, "recorded"), RecordedFormat: "<id>.m2ts"}
	finalPath := filepath.Join(cfg.RecordedDir, "retry.m2ts")
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(finalPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Reserves, []legacy.Program{program}, false); err != nil {
		t.Fatal(err)
	}

	result, err := runOnceTest(t, context.Background(), paths, cfg, &fakeStreamer{body: "new"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Completed != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("retry output = %q, want %q", data, "new")
	}
	if _, err := os.Stat(finalPath + ".part"); !os.IsNotExist(err) {
		t.Fatalf("temporary retry output still exists: %v", err)
	}
}

func TestReplaceRecordingOutputReplacesExistingFinalPath(t *testing.T) {
	dir := t.TempDir()
	finalPath := filepath.Join(dir, "recorded.m2ts")
	partPath := finalPath + ".part"
	if err := os.WriteFile(finalPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partPath, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := replaceRecordingOutput(partPath, finalPath); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("replaced output = %q, want %q", data, "new")
	}
	if _, err := os.Stat(partPath); !os.IsNotExist(err) {
		t.Fatalf("part output still exists: %v", err)
	}
}

func TestReplaceRecordingOutputMissingPartKeepsExistingFinalPath(t *testing.T) {
	dir := t.TempDir()
	finalPath := filepath.Join(dir, "recorded.m2ts")
	partPath := finalPath + ".part"
	if err := os.WriteFile(finalPath, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := replaceRecordingOutput(partPath, finalPath); err == nil {
		t.Fatal("missing part output was accepted")
	}
	data, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old" {
		t.Fatalf("existing final output = %q, want %q", data, "old")
	}
}

func TestUpdateRecordingProgramPreservesLatestAbortAndExternalFields(t *testing.T) {
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "data", "strata.db")
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		t.Fatal(err)
	}
	current := legacy.Program{
		ID:    "active",
		Title: "Current",
		Abort: true,
		Raw: map[string]json.RawMessage{
			"external": json.RawMessage(`{"value":"keep"}`),
		},
	}
	if err := programstore.Upsert(context.Background(), databasePath, programstore.Recording, current); err != nil {
		t.Fatal(err)
	}
	update := legacy.Program{
		ID:       current.ID,
		Recorded: filepath.ToSlash(filepath.Join(dir, "recorded", "active.m2ts")),
		PID:      -1,
		Raw: map[string]json.RawMessage{
			"priority": json.RawMessage(`2`),
		},
	}
	if err := updateRecordingProgram(context.Background(), databasePath, update); err != nil {
		t.Fatal(err)
	}
	got, found, err := programstore.ReadByID(context.Background(), databasePath, programstore.Recording, current.ID)
	if err != nil || !found {
		t.Fatalf("updated recording found=%v err=%v", found, err)
	}
	if !got.Abort {
		t.Fatalf("latest abort flag was lost: %#v", got)
	}
	if string(got.Raw["external"]) != `{"value":"keep"}` {
		t.Fatalf("latest external field was lost: %s", got.Raw["external"])
	}
	if got.Recorded != update.Recorded || got.PID != update.PID || string(got.Raw["priority"]) != "2" {
		t.Fatalf("recorder metadata was not applied: %#v", got)
	}
}

func TestCompletePreservesLatestRecordingState(t *testing.T) {
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "data", "strata.db")
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		t.Fatal(err)
	}
	current := legacy.Program{
		ID:    "complete",
		Abort: true,
		Raw:   map[string]json.RawMessage{"external": json.RawMessage(`{"keep":true}`)},
	}
	if err := programstore.Upsert(context.Background(), databasePath, programstore.Recording, current); err != nil {
		t.Fatal(err)
	}
	if err := programstore.Complete(context.Background(), databasePath, legacy.Program{
		ID:       current.ID,
		Recorded: "complete.m2ts",
		Abort:    false,
	}); err != nil {
		t.Fatal(err)
	}
	recorded, err := programstore.Read(context.Background(), databasePath, programstore.Recorded)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || !recorded[0].Abort || string(recorded[0].Raw["external"]) != `{"keep":true}` {
		t.Fatalf("completed program lost latest state: %#v", recorded)
	}
}

func TestCompleteRefusesMissingRecording(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "data", "strata.db")
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		t.Fatal(err)
	}
	err := programstore.Complete(context.Background(), databasePath, legacy.Program{ID: "missing"})
	if !errors.Is(err, programstore.ErrProgramNotFound) {
		t.Fatalf("missing completion error = %v, want ErrProgramNotFound", err)
	}
}

func TestFinishRecordingMissingActiveRowKeepsReservation(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	paths := testPaths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
		Log:       filepath.Join(dir, "log", "operator"),
	}
	if err := os.MkdirAll(filepath.Dir(paths.Recording), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.Reserves, paths.Recording, paths.Recorded} {
		if err := storage.WriteJSONAtomic(path, []legacy.Program{}, false); err != nil {
			t.Fatal(err)
		}
	}
	paths = seedOperatorTestDatabase(t, paths)
	program := legacy.Program{ID: "missing", Start: now.UnixMilli(), End: now.Add(time.Hour).UnixMilli()}
	if err := reservationstore.Write(context.Background(), paths.Database, []legacy.Program{program}); err != nil {
		t.Fatal(err)
	}
	if err := programstore.Write(context.Background(), paths.Database, programstore.Recording, nil); err != nil {
		t.Fatal(err)
	}
	finishRecording(context.Background(), paths.runtime(), &config.Config{
		RecordedDir:    filepath.Join(dir, "recorded"),
		RecordedFormat: "<id>.m2ts",
	}, &fakeStreamer{body: "stale"}, program)
	recorded, err := programstore.Read(context.Background(), paths.Database, programstore.Recorded)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 0 {
		t.Fatalf("stale recording was promoted: %#v", recorded)
	}
	reserves, err := reservationstore.Read(context.Background(), paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 1 || reserves[0].ID != program.ID {
		t.Fatalf("reservation was removed after missing active row: %#v", reserves)
	}
}

func TestFinishRecordingCompleteFailureRemovesActiveAndOutput(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	paths := seedOperatorTestDatabase(t, testPaths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
		Log:       filepath.Join(dir, "log", "operator"),
	})
	program := legacy.Program{
		ID:      "cfail",
		Start:   time.Now().Add(-time.Minute).UnixMilli(),
		End:     time.Now().Add(time.Hour).UnixMilli(),
		Channel: legacy.Channel{Type: "GR", Channel: "27", Name: "Service"},
	}
	if err := reservationstore.Write(context.Background(), paths.Database, []legacy.Program{program}); err != nil {
		t.Fatal(err)
	}
	if err := programstore.Write(context.Background(), paths.Database, programstore.Recording, []legacy.Program{program}); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(context.Background(), `CREATE TRIGGER fail_recorded_insert
		BEFORE INSERT ON program_collections
		WHEN NEW.collection = 'recorded'
		BEGIN SELECT RAISE(ABORT, 'injected completion failure'); END;`)
	if closeErr := db.Close(); err != nil {
		t.Fatal(err)
	} else if closeErr != nil {
		t.Fatal(closeErr)
	}

	cfg := &config.Config{RecordedDir: filepath.Join(dir, "recorded"), RecordedFormat: "<id>.m2ts"}
	finishRecording(context.Background(), paths.runtime(), cfg, &fakeStreamer{body: "ts"}, program)

	active, err := programstore.Read(context.Background(), paths.Database, programstore.Recording)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("active recording was left after completion failure: %#v", active)
	}
	recorded, err := programstore.Read(context.Background(), paths.Database, programstore.Recorded)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 0 {
		t.Fatalf("failed completion was promoted: %#v", recorded)
	}
	reserves, err := reservationstore.Read(context.Background(), paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 1 || reserves[0].ID != program.ID {
		t.Fatalf("reservation was not retained: %#v", reserves)
	}
	finalPath := filepath.Join(cfg.RecordedDir, "cfail.m2ts")
	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Fatalf("failed completion output still exists: %v", err)
	}
}

func TestPendingCompletionRollbackRetriesAfterDatabaseRecovers(t *testing.T) {
	dir := t.TempDir()
	paths := testPaths{Database: filepath.Join(dir, "data", "strata.db")}
	if err := os.MkdirAll(filepath.Dir(paths.Database), 0o755); err != nil {
		t.Fatal(err)
	}
	program := legacy.Program{
		ID:       "retry",
		Recorded: filepath.Join(dir, "recorded", "retry.m2ts"),
		PID:      0,
		Raw:      map[string]json.RawMessage{"_strataFinalizing": json.RawMessage(`"stale-token"`)},
	}
	if err := programstore.Upsert(context.Background(), paths.Database, programstore.Recording, program); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(program.Recorded), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program.Recorded, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(context.Background(), `CREATE TRIGGER fail_recording_delete
		BEFORE DELETE ON program_collections
		WHEN OLD.collection = 'recording'
		BEGIN SELECT RAISE(ABORT, 'injected delete failure'); END;`)
	if err == nil {
		_, err = db.ExecContext(context.Background(), `CREATE TRIGGER fail_recording_update
			BEFORE UPDATE ON program_collections
			WHEN OLD.collection = 'recording'
			BEGIN SELECT RAISE(ABORT, 'injected update failure'); END;`)
	}
	if closeErr := db.Close(); err != nil {
		t.Fatal(err)
	} else if closeErr != nil {
		t.Fatal(closeErr)
	}
	if err := rollbackCompletedRecording(context.Background(), paths.Database, program); err == nil {
		t.Fatal("rollback unexpectedly succeeded while database trigger was active")
	}

	db, err = database.Open(context.Background(), paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), "DROP TRIGGER fail_recording_delete"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), "DROP TRIGGER fail_recording_update"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	retryPendingCompletionRollbacks(context.Background(), paths.Database)
	active, err := programstore.Read(context.Background(), paths.Database, programstore.Recording)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("pending completion rollback did not remove active row: %#v", active)
	}
	if _, err := os.Stat(program.Recorded); !os.IsNotExist(err) {
		t.Fatalf("pending completion rollback left output: %v", err)
	}
}

func TestPendingCompletionRollbackDoesNotDeleteNewRecording(t *testing.T) {
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "data", "strata.db")
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		t.Fatal(err)
	}
	old := legacy.Program{ID: "retry", Recorded: filepath.Join(dir, "recorded", "retry.m2ts"), PID: 0}
	queueCompletionRollback(databasePath, old)
	current := old
	current.PID = -1
	if err := programstore.Upsert(context.Background(), databasePath, programstore.Recording, current); err != nil {
		t.Fatal(err)
	}
	retryPendingCompletionRollbacks(context.Background(), databasePath)
	active, err := programstore.Read(context.Background(), databasePath, programstore.Recording)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].PID != -1 {
		t.Fatalf("new recording was removed by stale rollback: %#v", active)
	}
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

func TestLowStorageStopIgnoresFinalizingRecording(t *testing.T) {
	dir := t.TempDir()
	oldGetDiskUsage := getDiskUsage
	getDiskUsage = func(string) (system.DiskUsage, error) {
		return system.DiskUsage{Avail: 10 * 1024 * 1024}, nil
	}
	defer func() { getDiskUsage = oldGetDiskUsage }()

	paths := testPaths{
		Database: filepath.Join(dir, "data", "strata.db"),
		Log:      filepath.Join(dir, "log", "operator"),
	}
	if err := os.MkdirAll(filepath.Dir(paths.Database), 0o755); err != nil {
		t.Fatal(err)
	}
	recording := []legacy.Program{{
		ID:  "finalizing",
		Raw: map[string]json.RawMessage{"_strataFinalizing": json.RawMessage(`true`)},
	}}
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
	updated, err := programstore.Read(context.Background(), paths.Database, programstore.Recording)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated) != 1 || updated[0].Abort {
		t.Fatalf("finalizing recording was aborted: %#v", updated)
	}
}
