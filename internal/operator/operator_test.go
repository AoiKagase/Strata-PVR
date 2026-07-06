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

	"chinachu-go/internal/chinachu"
	"chinachu-go/internal/config"
	"chinachu-go/internal/storage"
	"chinachu-go/internal/system"
)

func TestMain(m *testing.M) {
	if output := os.Getenv("CHINACHU_GO_RECORDED_COMMAND_OUTPUT"); output != "" && len(os.Args) == 3 {
		_ = os.WriteFile(output, []byte(os.Args[1]+"\n"+os.Args[2]), 0o644)
		os.Exit(0)
	}
	if output := os.Getenv("CHINACHU_GO_SENDMAIL_OUTPUT"); output != "" && len(os.Args) == 2 && os.Args[1] == "-t" {
		data, _ := io.ReadAll(os.Stdin)
		_ = os.WriteFile(output, data, 0o644)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

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

func TestRunOnceRecordsDueProgram(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	paths := Paths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
		Log:       filepath.Join(dir, "log", "operator"),
	}
	program := chinachu.Program{
		ID:       "21i3v9",
		Title:    "Test/Program",
		Start:    now.Add(10 * time.Second).UnixMilli(),
		End:      now.Add(time.Hour).UnixMilli(),
		Category: "anime",
		Channel:  chinachu.Channel{Type: "GR", Channel: "27", Name: "Service"},
	}
	if err := storage.WriteJSONAtomic(paths.Reserves, []chinachu.Program{program}, false); err != nil {
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
	var reserves []chinachu.Program
	if err := storage.ReadJSON(paths.Reserves, &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 0 {
		t.Fatalf("reserves not cleared: %#v", reserves)
	}
	var recording []chinachu.Program
	if err := storage.ReadJSON(paths.Recording, &recording, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(recording) != 0 {
		t.Fatalf("recording not cleared: %#v", recording)
	}
	var recorded []chinachu.Program
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
		"FIN: #21i3v9 ",
	} {
		if !strings.Contains(string(logData), want) {
			t.Fatalf("operator log missing %q: %s", want, string(logData))
		}
	}
}

func TestRecordProgramSetsMirakurunPriority(t *testing.T) {
	dir := t.TempDir()
	recordingPath := filepath.Join(dir, "data", "recording.json")
	if err := storage.WriteJSONAtomic(recordingPath, []chinachu.Program{}, false); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		RecordedDir:        filepath.Join(dir, "recorded"),
		RecordedFormat:     "<id>.m2ts",
		RecordingPriority:  5,
		ConflictedPriority: 1,
	}
	program := chinachu.Program{ID: "1", Title: "Priority"}
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

func TestRunOnceSkipsConflictAndFuturePrograms(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	paths := Paths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
	}
	reserves := []chinachu.Program{
		{ID: "a", Start: now.Add(time.Minute).UnixMilli(), End: now.Add(time.Hour).UnixMilli()},
		{ID: "b", Start: now.UnixMilli(), End: now.Add(time.Hour).UnixMilli(), IsSkip: true},
		{ID: "c", Start: now.UnixMilli(), End: now.Add(time.Hour).UnixMilli(), IsConflict: true},
	}
	if err := storage.WriteJSONAtomic(paths.Reserves, reserves, false); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{RecordedDir: filepath.Join(dir, "recorded"), RecordedFormat: "<id>.m2ts"}
	result, err := RunOnce(context.Background(), paths, cfg, &fakeStreamer{body: "x"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Started != 0 || result.Completed != 0 || result.Failed != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestMergeRecordedProgramMatchesLegacyDuplicateHandling(t *testing.T) {
	completed := chinachu.Program{ID: "same", Start: 2000, Recorded: "/recorded/new.m2ts"}
	recorded := mergeRecordedProgram([]chinachu.Program{
		{ID: "same", Start: 1000, Recorded: "/recorded/new.m2ts"},
		{ID: "other", Recorded: "/recorded/other.m2ts"},
	}, completed)
	if len(recorded) != 2 || recorded[0].ID != "other" || recorded[1].ID != "same" {
		t.Fatalf("same path merge mismatch: %#v", recorded)
	}

	recorded = mergeRecordedProgram([]chinachu.Program{
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
	}
	program := chinachu.Program{
		ID:      "21i3v9",
		Title:   "Abortable",
		Start:   now.UnixMilli(),
		End:     now.Add(time.Hour).UnixMilli(),
		Channel: chinachu.Channel{Type: "GR", Channel: "27", Name: "Service"},
	}
	if err := storage.WriteJSONAtomic(paths.Reserves, []chinachu.Program{program}, false); err != nil {
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

	var recording []chinachu.Program
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if err := storage.ReadJSON(paths.Recording, &recording, "[]"); err != nil {
			t.Fatal(err)
		}
		if len(recording) == 1 {
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
	var recorded []chinachu.Program
	if err := storage.ReadJSON(paths.Recorded, &recorded, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || recorded[0].Recorded == "" {
		t.Fatalf("recorded entry missing after abort: %#v", recorded)
	}
}

func TestRunOnceStartsRecordedCommandWithFileAndProgramJSON(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	paths := Paths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
	}
	program := chinachu.Program{
		ID:      "21i3v9",
		Title:   "Hooked",
		Start:   now.UnixMilli(),
		End:     now.Add(time.Hour).UnixMilli(),
		Channel: chinachu.Channel{Type: "GR", Channel: "27", Name: "Service"},
	}
	if err := storage.WriteJSONAtomic(paths.Reserves, []chinachu.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "recorded-command.txt")
	t.Setenv("CHINACHU_GO_RECORDED_COMMAND_OUTPUT", output)
	cfg := &config.Config{
		RecordedDir:     filepath.Join(dir, "recorded"),
		RecordedFormat:  "<id>.m2ts",
		RecordedCommand: os.Args[0],
	}
	result, err := RunOnce(context.Background(), paths, cfg, &fakeStreamer{body: "tsdata"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Completed != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	var content []byte
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		content, err = os.ReadFile(output)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(string(content), "\n", 2)
	if len(lines) != 2 {
		t.Fatalf("unexpected command output: %q", content)
	}
	if !strings.HasSuffix(filepath.ToSlash(lines[0]), "/recorded/21i3v9.m2ts") {
		t.Fatalf("unexpected recorded command path: %s", lines[0])
	}
	var passed chinachu.Program
	if err := json.Unmarshal([]byte(lines[1]), &passed); err != nil {
		t.Fatal(err)
	}
	if passed.ID != program.ID || passed.Recorded == "" {
		t.Fatalf("unexpected recorded command payload: %#v", passed)
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
	if err := storage.WriteJSONAtomic(paths.Reserves, []chinachu.Program{}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(paths.Recording, []chinachu.Program{}, false); err != nil {
		t.Fatal(err)
	}
	recorded := []chinachu.Program{{ID: "old", Recorded: filepath.ToSlash(oldFile)}, {ID: "new", Recorded: filepath.ToSlash(filepath.Join(dir, "recorded", "new.m2ts"))}}
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
	var remaining []chinachu.Program
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
	var backup []chinachu.Program
	if err := storage.ReadJSON(backups[0], &backup, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(backup) != 2 {
		t.Fatalf("backup should contain original recorded list: %#v", backup)
	}
}

func TestLowStorageSendsNotification(t *testing.T) {
	dir := t.TempDir()
	lowStorageLastNotified = time.Time{}
	oldGetDiskUsage := getDiskUsage
	getDiskUsage = func(string) (system.DiskUsage, error) {
		return system.DiskUsage{Avail: 42 * 1024 * 1024}, nil
	}
	defer func() { getDiskUsage = oldGetDiskUsage }()
	oldSendmailPath := sendmailPath
	sendmailPath = os.Args[0]
	defer func() { sendmailPath = oldSendmailPath }()
	output := filepath.Join(dir, "sendmail.txt")
	t.Setenv("CHINACHU_GO_SENDMAIL_OUTPUT", output)
	recordedDir := filepath.Join(dir, "recorded")
	if err := os.MkdirAll(recordedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := Paths{Log: filepath.Join(dir, "log", "operator")}
	cfg := &config.Config{
		RecordedDir:                recordedDir,
		StorageLowSpaceThresholdMB: 100,
		StorageLowSpaceNotifyTo:    "admin@example.test",
	}

	if _, err := handleLowStorage(context.Background(), paths, cfg, nil, nil); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	message := string(data)
	for _, want := range []string{
		"From: Chinachu <chinachu@localhost>",
		"To: admin@example.test",
		"Subject: [Chinachu] ALERT: Storage Low Space!",
		"Current Free Space is 42 MB.",
		"Threshold is 100 MB.",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("notification missing %q: %q", want, message)
		}
	}
}

func TestLowStorageNotificationIsThrottled(t *testing.T) {
	dir := t.TempDir()
	oldGetDiskUsage := getDiskUsage
	getDiskUsage = func(string) (system.DiskUsage, error) {
		return system.DiskUsage{Avail: 42 * 1024 * 1024}, nil
	}
	defer func() { getDiskUsage = oldGetDiskUsage }()
	oldSendmailPath := sendmailPath
	sendmailPath = os.Args[0]
	defer func() { sendmailPath = oldSendmailPath }()
	baseTime := time.Unix(1000, 0)
	oldLowStorageNow := lowStorageNow
	lowStorageNow = func() time.Time { return baseTime }
	defer func() { lowStorageNow = oldLowStorageNow }()
	lowStorageLastNotified = time.Time{}
	defer func() { lowStorageLastNotified = time.Time{} }()
	output := filepath.Join(dir, "sendmail.txt")
	t.Setenv("CHINACHU_GO_SENDMAIL_OUTPUT", output)
	recordedDir := filepath.Join(dir, "recorded")
	if err := os.MkdirAll(recordedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := Paths{Log: filepath.Join(dir, "log", "operator")}
	cfg := &config.Config{
		RecordedDir:                recordedDir,
		StorageLowSpaceThresholdMB: 100,
		StorageLowSpaceNotifyTo:    "admin@example.test",
	}

	if _, err := handleLowStorage(context.Background(), paths, cfg, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(output); err != nil {
		t.Fatal(err)
	}
	if _, err := handleLowStorage(context.Background(), paths, cfg, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("notification was not throttled: %v", err)
	}
	baseTime = baseTime.Add(lowStorageNotifyInterval + time.Second)
	if _, err := handleLowStorage(context.Background(), paths, cfg, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("notification was not sent after interval: %v", err)
	}
}
