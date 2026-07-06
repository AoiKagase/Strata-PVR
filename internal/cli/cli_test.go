package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chinachu-go/internal/chinachu"
	"chinachu-go/internal/storage"
)

func TestHelp(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), nil, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "reserve <pgid>") {
		t.Fatalf("help missing reserve: %s", out.String())
	}
}

func TestUpdaterAcceptedWithoutNodeRuntime(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"updater"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "automatic git/service/installer operations are intentionally not performed") || !strings.Contains(text, "Node.js/npm modules are not required") {
		t.Fatalf("unexpected updater output: %s", text)
	}
}

func TestServiceInitscriptIncludesRestart(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"service", "operator", "initscript"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"### BEGIN INIT INFO",
		"# Provides:          chinachu-operator",
		"DAEMON=./chinachu-go",
		`DAEMON_OPTS="service operator execute"`,
		"USER=$USER",
		"test -x $DAEMON || exit 0",
		`PID=$(su $USER -c "exec $DAEMON $DAEMON_OPTS < /dev/null > /dev/null 2>&1 & echo \$!")`,
		"PGID=$(ps -p $PID -o pgrp | grep -v PGRP)",
		"kill -QUIT -$(echo $PGID)",
		"restart )",
		"sleep 3",
		"Usage: $NAME {start|stop|restart|status}",
		"exit 0",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("initscript missing %q: %s", want, text)
		}
	}
}

func TestPrepareServiceRuntimeCopiesSamplesAndCreatesDirs(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("config.sample.json", []byte(`{"sample":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("rules.sample.json", []byte(`[{"sample":true}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepareServiceRuntime(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"config.json", "rules.json", "log", "data"} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s was not prepared: %v", path, err)
		}
	}
	if data, _ := os.ReadFile("config.json"); string(data) != `{"sample":true}` {
		t.Fatalf("config.json = %q", data)
	}
	if data, _ := os.ReadFile("rules.json"); string(data) != `[{"sample":true}]` {
		t.Fatalf("rules.json = %q", data)
	}
}

func TestPrepareServiceRuntimeDoesNotOverwriteExistingFiles(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"config.sample.json": `{"sample":true}`,
		"rules.sample.json":  `[{"sample":true}]`,
		"config.json":        `{"existing":true}`,
		"rules.json":         `[{"existing":true}]`,
	} {
		if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := prepareServiceRuntime(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile("config.json"); string(data) != `{"existing":true}` {
		t.Fatalf("config.json was overwritten: %q", data)
	}
	if data, _ := os.ReadFile("rules.json"); string(data) != `[{"existing":true}]` {
		t.Fatalf("rules.json was overwritten: %q", data)
	}
}

func TestTestCommandAcceptedWithoutUsrBinExecution(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"test", "ffmpeg", "-version"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "usr/bin/ffmpeg is not executed") || !strings.Contains(text, "Node.js/npm modules are not required") {
		t.Fatalf("unexpected test command output: %s", text)
	}
}

func TestIRCBotAcceptedAsUnimplementedGoRuntimeFeature(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"ircbot"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "experimental Node-era IRC bot is not implemented") || !strings.Contains(text, "Go API") {
		t.Fatalf("unexpected ircbot output: %s", text)
	}
}

func TestCompatCheckValidatesStateFilesAndRecordedDir(t *testing.T) {
	dir := t.TempDir()
	mirakurun := newCompatMirakurun(t)
	defer mirakurun.Close()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("recorded", 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"config.json":         `{"recordedDir":"recorded","mirakurunPath":"` + mirakurun.URL + `"}`,
		"rules.json":          `[]`,
		"data/schedule.json":  `[]`,
		"data/reserves.json":  `[]`,
		"data/recording.json": `[]`,
		"data/recorded.json":  `[]`,
	}
	for name, data := range files {
		if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"compat", "check"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"OK config.json",
		"OK rules.json",
		"OK data directory",
		"OK recordedDir",
		"OK data/schedule.json",
		"OK data/reserves.json",
		"OK data/recording.json",
		"OK data/recorded.json",
		"OK available disk space",
		"OK Mirakurun services",
		"OK Mirakurun programs",
		"OK Mirakurun tuners",
		"OK Node.js runtime",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compat output missing %q: %s", want, text)
		}
	}
}

func TestCompatCheckFailsWhenStateFileMissing(t *testing.T) {
	dir := t.TempDir()
	mirakurun := newCompatMirakurun(t)
	defer mirakurun.Close()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("recorded", 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"config.json":         `{"recordedDir":"recorded","mirakurunPath":"` + mirakurun.URL + `"}`,
		"rules.json":          `[]`,
		"data/reserves.json":  `[]`,
		"data/recording.json": `[]`,
		"data/recorded.json":  `[]`,
	} {
		if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	err := Run(context.Background(), []string{"compat", "doctor"}, &out, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "compat check failed") {
		t.Fatalf("expected compat failure, got err=%v output=%s", err, out.String())
	}
	if !strings.Contains(out.String(), "NG data/schedule.json") {
		t.Fatalf("compat output missing missing schedule failure: %s", out.String())
	}
}

func TestCompatCheckFailsWhenMirakurunUnavailable(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("recorded", 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"config.json":         `{"recordedDir":"recorded","mirakurunPath":"http://127.0.0.1:1"}`,
		"rules.json":          `[]`,
		"data/schedule.json":  `[]`,
		"data/reserves.json":  `[]`,
		"data/recording.json": `[]`,
		"data/recorded.json":  `[]`,
	} {
		if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	err := Run(context.Background(), []string{"compat", "check"}, &out, &bytes.Buffer{})
	if err == nil {
		t.Fatalf("expected Mirakurun failure, output=%s", out.String())
	}
	if !strings.Contains(out.String(), "NG Mirakurun services") {
		t.Fatalf("compat output missing Mirakurun failure: %s", out.String())
	}
}

func TestCompatBackupCopiesExistingStateFiles(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"config.json":         `{"config":true}`,
		"rules.json":          `[{"rule":true}]`,
		"data/schedule.json":  `[{"schedule":true}]`,
		"data/reserves.json":  `[{"reserve":true}]`,
		"data/recording.json": `[{"recording":true}]`,
		"data/recorded.json":  `[{"recorded":true}]`,
	}
	for name, data := range files {
		if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"compat", "backup"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "OK backup: backup"+string(os.PathSeparator)+"chinachu-go-") {
		t.Fatalf("backup output missing success path: %s", out.String())
	}
	for name, want := range files {
		matches, err := filepath.Glob(filepath.Join("backup", "chinachu-go-*", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 1 {
			t.Fatalf("backup for %s matches = %v", name, matches)
		}
		data, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("backup %s = %q, want %q", matches[0], data, want)
		}
	}
}

func TestCompatBackupSkipsMissingOptionalStateFiles(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("config.json", []byte(`{"config":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"compat", "backup"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "BACKUP config.json ->") || !strings.Contains(text, "SKIP rules.json: not found") || !strings.Contains(text, "OK backup:") {
		t.Fatalf("unexpected backup output: %s", text)
	}
}

func newCompatMirakurun(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/services", "/api/programs", "/api/tuners":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestProgramListPrintsLegacyColumns(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	programs := []chinachu.Program{
		{
			ID:       "later",
			Title:    "Late",
			Category: "anime",
			Start:    time.Date(2026, 1, 2, 3, 4, 0, 0, time.Local).UnixMilli(),
			End:      time.Date(2026, 1, 2, 3, 34, 0, 0, time.Local).UnixMilli(),
			Seconds:  1800,
			Channel:  chinachu.Channel{Type: "GR", Channel: "27", SID: 101},
		},
		{
			ID:               "earlier",
			Title:            "Early",
			Category:         "news",
			Start:            time.Date(2026, 1, 1, 1, 2, 0, 0, time.Local).UnixMilli(),
			End:              time.Date(2026, 1, 1, 1, 32, 0, 0, time.Local).UnixMilli(),
			Seconds:          1800,
			IsManualReserved: true,
			Channel:          chinachu.Channel{Type: "BS", Channel: "BS1", SID: 201},
		},
	}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "reserves.json"), programs, false); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"reserves"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"Program ID", "Type:CH", "Cat", "By", "Datetime", "Dur", "Title"} {
		if !strings.Contains(text, want) {
			t.Fatalf("program list missing %q: %s", want, text)
		}
	}
	if !strings.Contains(text, "0\tearlier\tBS:BS1\tnews\tuser") {
		t.Fatalf("manual reserve row missing or unsorted: %s", text)
	}
	if !strings.Contains(text, "1\tlater\tGR:27\tanime\trule") {
		t.Fatalf("auto reserve row missing: %s", text)
	}
}

func TestCleanupSimulationKeepsRecordedList(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dir, "exists.m2ts")
	if err := os.WriteFile(existing, []byte("ts"), 0o644); err != nil {
		t.Fatal(err)
	}
	recorded := []chinachu.Program{
		{ID: "exists", Recorded: filepath.ToSlash(existing)},
		{ID: "missing", Recorded: filepath.ToSlash(filepath.Join(dir, "missing.m2ts"))},
	}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "recorded.json"), recorded, false); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"cleanup", "--simulation"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"action\tProgram ID\tRecorded",
		"exist\texists\t",
		"[simulation] removed\tmissing\t",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("cleanup output missing %q: %s", want, text)
		}
	}
	var got []chinachu.Program
	if err := storage.ReadJSON(filepath.Join("data", "recorded.json"), &got, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("simulation should keep recorded list: %#v", got)
	}
	backups, err := filepath.Glob(filepath.Join("data", "recorded.json.bak-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("simulation should not create backups: %#v", backups)
	}
}

func TestCleanupBacksUpRecordedListBeforeRemoval(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dir, "exists.m2ts")
	if err := os.WriteFile(existing, []byte("ts"), 0o644); err != nil {
		t.Fatal(err)
	}
	recorded := []chinachu.Program{
		{ID: "exists", Recorded: filepath.ToSlash(existing)},
		{ID: "missing", Recorded: filepath.ToSlash(filepath.Join(dir, "missing.m2ts"))},
	}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "recorded.json"), recorded, false); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"cleanup"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(filepath.Join("data", "recorded.json.bak-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("backup count = %d, backups=%#v", len(backups), backups)
	}
	var backup []chinachu.Program
	if err := storage.ReadJSON(backups[0], &backup, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(backup) != 2 {
		t.Fatalf("backup should contain original list: %#v", backup)
	}
	var got []chinachu.Program
	if err := storage.ReadJSON(filepath.Join("data", "recorded.json"), &got, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "exists" {
		t.Fatalf("cleanup should remove only missing entry: %#v", got)
	}
}

func TestRulesPrintsLegacyTable(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	rules := []chinachu.Rule{
		{
			Types:         []string{"GR"},
			Categories:    []string{"anime"},
			ReserveTitles: []string{"ニュース", "映画"},
			Hour:          &chinachu.RangeRule{Start: 1, End: 4},
		},
	}
	if err := storage.WriteJSONAtomic("rules.json", rules, false); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"rules"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"#\t0", "types\tGR", "categories\tanime", "hour\t1, 4", "reserve_titles\t[2]"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rules output missing %q: %s", want, text)
		}
	}
	out.Reset()
	if err := Run(context.Background(), []string{"rules", "-detail"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ニュース, 映画") {
		t.Fatalf("detailed rules output missing titles: %s", out.String())
	}
	rules = append(rules, chinachu.Rule{Types: []string{"BS"}})
	if err := storage.WriteJSONAtomic("rules.json", rules, false); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"rules"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "#\ttypes\tcategories") || !strings.Contains(out.String(), "1\tBS\t-") {
		t.Fatalf("multi-rule table output missing: %s", out.String())
	}
}

func TestReserve(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	schedule := `[{"type":"GR","channel":"27","name":"svc","id":"s","sid":101,"programs":[{"id":"p1","title":"T","fullTitle":"T","start":1,"end":2,"seconds":1,"channel":{"type":"GR","channel":"27","name":"svc","id":"s","sid":101}}]}]`
	if err := os.WriteFile(filepath.Join("data", "schedule.json"), []byte(schedule), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "reserves.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"reserve", "p1", "--1seg"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join("data", "reserves.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"isManualReserved":true`) {
		t.Fatalf("reserve file not updated: %s", string(b))
	}
	if !strings.Contains(string(b), `"1seg":true`) {
		t.Fatalf("1seg flag not written: %s", string(b))
	}
}

func TestReserveSimulationDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	schedule := `[{"type":"GR","channel":"27","name":"svc","id":"s","sid":101,"programs":[{"id":"p1","title":"T","start":1,"end":2,"seconds":1,"channel":{"type":"GR","channel":"27","name":"svc","id":"s","sid":101}}]}]`
	if err := os.WriteFile(filepath.Join("data", "schedule.json"), []byte(schedule), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "reserves.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"reserve", "p1", "-s", "--1seg"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "[simulation] reserve:") || !strings.Contains(out.String(), `"1seg": true`) {
		t.Fatalf("unexpected simulation output: %s", out.String())
	}
	b, err := os.ReadFile(filepath.Join("data", "reserves.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(b)) != "[]" {
		t.Fatalf("simulation wrote reserves: %s", string(b))
	}
}

func TestReserveAcceptsLegacyIDOption(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	schedule := `[{"type":"GR","channel":"27","name":"svc","id":"s","sid":101,"programs":[{"id":"p1","title":"T","start":1,"end":2,"seconds":1,"channel":{"type":"GR","channel":"27","name":"svc","id":"s","sid":101}}]}]`
	if err := os.WriteFile(filepath.Join("data", "schedule.json"), []byte(schedule), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "reserves.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"reserve", "-s", "-id", "p1", "--1seg"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var reserves []chinachu.Program
	if err := storage.ReadJSON(filepath.Join("data", "reserves.json"), &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 0 {
		t.Fatalf("simulation should not write reserves: %#v", reserves)
	}
	if err := Run(context.Background(), []string{"reserve", "--id=p1", "--1seg"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	reserves = nil
	if err := storage.ReadJSON(filepath.Join("data", "reserves.json"), &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 1 || reserves[0].ID != "p1" || !reserves[0].OneSeg {
		t.Fatalf("reserve did not use legacy id option: %#v", reserves)
	}
}

func TestReserveAcceptsFlagsBeforePositionalID(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	schedule := `[{"type":"GR","channel":"27","name":"svc","id":"s","sid":101,"programs":[{"id":"p1","title":"T","start":1,"end":2,"seconds":1,"channel":{"type":"GR","channel":"27","name":"svc","id":"s","sid":101}}]}]`
	if err := os.WriteFile(filepath.Join("data", "schedule.json"), []byte(schedule), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "reserves.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"reserve", "-s", "p1"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "[simulation] reserve:") {
		t.Fatalf("unexpected output: %s", out.String())
	}
	var reserves []chinachu.Program
	if err := storage.ReadJSON(filepath.Join("data", "reserves.json"), &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 0 {
		t.Fatalf("simulation should not write reserves: %#v", reserves)
	}
}

func TestLegacyModeOptionDispatch(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	schedule := `[{"type":"GR","channel":"27","name":"svc","id":"s","sid":101,"programs":[{"id":"p1","title":"T","fullTitle":"T","start":1,"end":2,"seconds":1,"channel":{"type":"GR","channel":"27","name":"svc","id":"s","sid":101}}]}]`
	if err := os.WriteFile(filepath.Join("data", "schedule.json"), []byte(schedule), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "reserves.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"-mode", "reserve", "-id", "p1"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var reserves []chinachu.Program
	if err := storage.ReadJSON(filepath.Join("data", "reserves.json"), &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 1 || reserves[0].ID != "p1" {
		t.Fatalf("legacy mode reserve failed: %#v", reserves)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"--mode=search", "-id", "p1"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "p1") {
		t.Fatalf("legacy mode search failed: %s", out.String())
	}
}

func TestReserveMutationsSimulationDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	initial := []chinachu.Program{
		{ID: "manual", IsManualReserved: true},
		{ID: "auto"},
		{ID: "skipped", IsSkip: true},
	}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "reserves.json"), initial, false); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"unreserve", "manual", "-s"}, "[simulation] unreserve:"},
		{[]string{"skip", "auto", "--simulation"}, "[simulation] skip:"},
		{[]string{"unskip", "skipped", "-s"}, "[simulation] skip:"},
	}
	for _, tt := range tests {
		var out bytes.Buffer
		if err := Run(context.Background(), tt.args, &out, &bytes.Buffer{}); err != nil {
			t.Fatalf("%v: %v", tt.args, err)
		}
		if !strings.Contains(out.String(), tt.want) {
			t.Fatalf("%v output missing %q: %s", tt.args, tt.want, out.String())
		}
	}
	var got []chinachu.Program
	if err := storage.ReadJSON(filepath.Join("data", "reserves.json"), &got, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(initial) || !got[2].IsSkip {
		t.Fatalf("simulation mutated reserves: %#v", got)
	}
}

func TestReserveMutationsAcceptLegacyIDOption(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	reserves := []chinachu.Program{{ID: "auto", Title: "Auto"}}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "reserves.json"), reserves, false); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"skip", "--id", "auto"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var got []chinachu.Program
	if err := storage.ReadJSON(filepath.Join("data", "reserves.json"), &got, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].IsSkip {
		t.Fatalf("skip did not use legacy id option: %#v", got)
	}
	if err := Run(context.Background(), []string{"unskip", "-id", "auto"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got = nil
	if err := storage.ReadJSON(filepath.Join("data", "reserves.json"), &got, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].IsSkip {
		t.Fatalf("unskip did not use legacy id option: %#v", got)
	}
}

func TestRuleLifecycle(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("rules.json", []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"rule", "-type", "GR", "-title", "笑点"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("rules.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"types": [`) || !strings.Contains(string(b), `"笑点"`) {
		t.Fatalf("rule not written: %s", string(b))
	}
	if err := Run(context.Background(), []string{"disrule", "0"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile("rules.json")
	if !strings.Contains(string(b), `"isDisabled": true`) {
		t.Fatalf("rule not disabled: %s", string(b))
	}
	if err := Run(context.Background(), []string{"rmrule", "0"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile("rules.json")
	if strings.TrimSpace(string(b)) != "[]" {
		t.Fatalf("rule not removed: %s", string(b))
	}
}

func TestRuleCommandDeletesNullMarkers(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	initial := `[{"types":["GR"],"reserve_titles":["Title"],"hour":{"start":1,"end":3},"duration":{"min":60,"max":3600}}]`
	if err := os.WriteFile("rules.json", []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"rule", "-n", "0", "-title", "null", "-start", "-1", "-mini", "-1"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("rules.json")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if strings.Contains(text, "reserve_titles") || strings.Contains(text, "hour") || strings.Contains(text, "duration") {
		t.Fatalf("rule deletion markers were not applied: %s", text)
	}
	if !strings.Contains(text, `"types": [`) || !strings.Contains(text, `"GR"`) {
		t.Fatalf("remaining rule condition was lost: %s", text)
	}
}

func TestSearchFiltersSchedule(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	schedule := `[{"type":"GR","channel":"27","name":"svc","id":"s","sid":101,"programs":[{"id":"p1","category":"anime","title":"Alpha","fullTitle":"Alpha","start":1893456000000,"end":1893457800000,"seconds":1800,"channel":{"type":"GR","channel":"27","name":"svc","id":"s","sid":101}},{"id":"p2","category":"news","title":"Beta","fullTitle":"Beta","start":1893459600000,"end":1893461400000,"seconds":1800,"channel":{"type":"GR","channel":"27","name":"svc","id":"s","sid":101}}]}]`
	if err := os.WriteFile(filepath.Join("data", "schedule.json"), []byte(schedule), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"search", "-title", "Alpha"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "p1") || strings.Contains(out.String(), "p2") {
		t.Fatalf("unexpected search output: %s", out.String())
	}
}

func TestStopMarksRecordingAbortAndAutoReserveSkip(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "recording.json"), []chinachu.Program{{ID: "auto"}, {ID: "manual", IsManualReserved: true}}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "reserves.json"), []chinachu.Program{{ID: "auto"}, {ID: "manual", IsManualReserved: true}}, false); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"stop", "auto"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "stop:") || !strings.Contains(out.String(), `"abort": true`) {
		t.Fatalf("unexpected stop output: %s", out.String())
	}
	var recording []chinachu.Program
	if err := storage.ReadJSON(filepath.Join("data", "recording.json"), &recording, "[]"); err != nil {
		t.Fatal(err)
	}
	if !recording[0].Abort {
		t.Fatalf("recording abort was not set: %#v", recording)
	}
	var reserves []chinachu.Program
	if err := storage.ReadJSON(filepath.Join("data", "reserves.json"), &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if !reserves[0].IsSkip || reserves[1].IsSkip {
		t.Fatalf("auto reserve skip was not updated correctly: %#v", reserves)
	}
}

func TestStopAcceptsLegacyIDOption(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	recording := []chinachu.Program{{ID: "rec", Title: "Recording"}}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "recording.json"), recording, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "reserves.json"), []chinachu.Program{{ID: "rec"}}, false); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"stop", "--id=rec"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var got []chinachu.Program
	if err := storage.ReadJSON(filepath.Join("data", "recording.json"), &got, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Abort {
		t.Fatalf("stop did not use legacy id option: %#v", got)
	}
}

func TestStopSimulationDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "recording.json"), []chinachu.Program{{ID: "auto"}}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "reserves.json"), []chinachu.Program{{ID: "auto"}}, false); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"stop", "auto", "--simulation"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "[simulation] stop:") || !strings.Contains(out.String(), `"abort": true`) {
		t.Fatalf("unexpected stop simulation output: %s", out.String())
	}
	var recording []chinachu.Program
	if err := storage.ReadJSON(filepath.Join("data", "recording.json"), &recording, "[]"); err != nil {
		t.Fatal(err)
	}
	if recording[0].Abort {
		t.Fatalf("simulation mutated recording: %#v", recording)
	}
	var reserves []chinachu.Program
	if err := storage.ReadJSON(filepath.Join("data", "reserves.json"), &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if reserves[0].IsSkip {
		t.Fatalf("simulation mutated reserves: %#v", reserves)
	}
}
