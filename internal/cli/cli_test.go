package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	passwordauth "strata-pvr/internal/auth"
	"strata-pvr/internal/config"
	"strata-pvr/internal/database"
	"strata-pvr/internal/legacy"
	"strata-pvr/internal/programstore"
	"strata-pvr/internal/reservationstore"
	"strata-pvr/internal/storage"
)

func TestHelp(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), nil, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "reserve <pgid>") || !strings.Contains(out.String(), "migrate") {
		t.Fatalf("help missing reserve: %s", out.String())
	}
}

func TestMigrateChinachuCreatesStrataDataAndArchivesInput(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join("migrate", "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyConfig := `{"mirakurunPath":"http://127.0.0.1:40772","recordedDir":"./recorded/","recordedFormat":"<title>.m2ts","wuiHost":"0.0.0.0","wuiPort":20772,"wuiUsers":["admin:secret"]}`
	if err := os.WriteFile(filepath.Join("migrate", "config.json"), []byte(legacyConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("migrate", "rules.json"), []byte(`[{"reserve_titles":["News"]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("migrate", "data", "recordings.json"), []byte(`[{"id":"active-1","start":100,"end":200}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("migrate", "data", "recorded.json"), []byte(`[{"id":"recorded-1","start":100,"end":200,"recorded":"video.m2ts"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("migrate", "data", "reserves.json"), []byte(`[{"id":"reserve-1","start":100,"end":200}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("migrate", "data", "schedule.json"), []byte(`[{"id":"channel-1","programs":[{"id":"program-1","start":100,"end":200}]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"migrate"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	doc, err := config.ParseDocument(mustReadCLIFile(t, filepath.Join("data", "config.json")))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Web.Authentication.Users) != 1 || !passwordauth.VerifyPassword(doc.Web.Authentication.Users[0].PasswordHash, "secret") {
		t.Fatal("legacy WUI password was not converted to Argon2id")
	}
	if _, err := os.Stat(filepath.Join("data", "strata.db")); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), filepath.Join("data", "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	rules, err := database.ReadRules(context.Background(), db)
	db.Close()
	if err != nil || len(rules) != 1 || !strings.Contains(string(rules[0]), "News") {
		t.Fatalf("rules were not imported into SQLite: %s %v", rules, err)
	}
	db, err = database.Open(context.Background(), filepath.Join("data", "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	reservations, err := database.ReadReservations(context.Background(), db)
	db.Close()
	if err != nil || len(reservations) != 1 || !strings.Contains(string(reservations[0]), "reserve-1") {
		t.Fatalf("reservations were not imported into SQLite: %s %v", reservations, err)
	}
	db, err = database.Open(context.Background(), filepath.Join("data", "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := database.ReadSchedule(context.Background(), db)
	db.Close()
	if err != nil || len(schedule) != 1 || schedule[0].ChannelKey != "channel-1" || len(schedule[0].Programs) != 1 {
		t.Fatalf("schedule was not imported into SQLite: %#v %v", schedule, err)
	}
	db, err = database.Open(context.Background(), filepath.Join("data", "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	active, activeErr := database.ReadProgramCollection(context.Background(), db, "recording")
	recorded, recordedErr := database.ReadProgramCollection(context.Background(), db, "recorded")
	db.Close()
	if activeErr != nil || recordedErr != nil || len(active) != 1 || len(recorded) != 1 || !strings.Contains(string(active[0]), "active-1") || !strings.Contains(string(recorded[0]), "recorded-1") {
		t.Fatalf("program collections were not imported: active=%s recorded=%s errors=%v/%v", active, recorded, activeErr, recordedErr)
	}
	archives, err := filepath.Glob(filepath.Join("backup", "chinachu-*", "config.json"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("legacy input was not archived: %v %v", archives, err)
	}
	if _, err := os.Stat("migrate"); !os.IsNotExist(err) {
		t.Fatalf("migrate input still exists: %v", err)
	}
	reports, err := filepath.Glob(filepath.Join("backup", "chinachu-*-report.json"))
	if err != nil || len(reports) != 1 {
		t.Fatalf("migration report = %v error=%v", reports, err)
	}
	var report struct {
		Version      int               `json:"version"`
		Imported     map[string]int    `json:"imported"`
		SourceSHA256 map[string]string `json:"sourceSha256"`
		SourceSize   map[string]int64  `json:"sourceSize"`
	}
	if err := json.Unmarshal(mustReadCLIFile(t, reports[0]), &report); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]int{"rules": 1, "reservations": 1, "scheduleChannels": 1, "schedulePrograms": 1, "recording": 1, "recorded": 1} {
		if report.Imported[key] != want {
			t.Fatalf("report imported[%s]=%d, want %d: %#v", key, report.Imported[key], want, report.Imported)
		}
	}
	if report.Version != 3 || len(report.SourceSHA256["config.json"]) != 64 || len(report.SourceSHA256["data/recordings.json"]) != 64 {
		t.Fatalf("migration report metadata = %#v", report)
	}
	archivedHashes, archivedSizes, err := inspectMigrationFiles(filepath.Dir(archives[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(report.SourceSHA256, archivedHashes) || !maps.Equal(report.SourceSize, archivedSizes) {
		t.Fatalf("migration report does not match archive: hashes=%v sizes=%v", report.SourceSHA256, report.SourceSize)
	}
}

func TestMigrateChinachuValidationFailureLeavesInputUntouched(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("migrate", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("migrate", "config.json"), []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"migration"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("migration accepted invalid config")
	}
	if _, err := os.Stat(filepath.Join("migrate", "config.json")); err != nil {
		t.Fatal("migration input was modified")
	}
	if _, err := os.Stat("data"); !os.IsNotExist(err) {
		t.Fatalf("partial data directory exists: %v", err)
	}
}

func TestMigrateChinachuCorruptDataLeavesInputUntouched(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join("migrate", "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyConfig := `{"mirakurunPath":"http://127.0.0.1:40772","recordedDir":"./recorded/","wuiOpenServer":true,"wuiOpenPort":20772}`
	if err := os.WriteFile(filepath.Join("migrate", "config.json"), []byte(legacyConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	corruptPath := filepath.Join("migrate", "data", "recordings.json")
	if err := os.WriteFile(corruptPath, []byte(`[{"id":"broken"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"migrate"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("migration accepted corrupt recording data")
	}
	if got := string(mustReadCLIFile(t, corruptPath)); got != `[{"id":"broken"}` {
		t.Fatalf("migration input changed: %q", got)
	}
	if _, err := os.Stat("data"); !os.IsNotExist(err) {
		t.Fatalf("partial data directory exists: %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join("backup", "chinachu-*")); len(matches) != 0 {
		t.Fatalf("failed migration created backup: %v", matches)
	}
}

func TestMigrateChinachuImportsLargeReservationSet(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join("migrate", "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyConfig := `{"mirakurunPath":"http://127.0.0.1:40772","recordedDir":"./recorded/","wuiOpenServer":true,"wuiOpenPort":20772}`
	if err := os.WriteFile(filepath.Join("migrate", "config.json"), []byte(legacyConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	const count = 2500
	reservations := make([]legacy.Program, count)
	for i := range reservations {
		reservations[i] = legacy.Program{ID: fmt.Sprintf("reservation-%04d", i), Start: int64(i * 1000), End: int64(i*1000 + 500)}
	}
	if err := storage.WriteJSONAtomic(filepath.Join("migrate", "data", "reserves.json"), reservations, false); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"migrate"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), filepath.Join("data", "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	documents, err := database.ReadReservations(context.Background(), db)
	if err != nil || len(documents) != count {
		t.Fatalf("large reservation import count=%d err=%v", len(documents), err)
	}
}

func TestConvertLegacyConfigWarnsAboutUnsupportedSettings(t *testing.T) {
	port := 20772
	_, warnings, err := convertLegacyConfig(&config.Config{
		WUIPort: &port, WUIHost: "127.0.0.1", WUIOpenServer: true,
		Raw: map[string]json.RawMessage{
			"wuiTlsKeyPath": []byte(`"server.key"`), "wuiXFF": []byte(`true`), "wuiAllowCountries": []byte(`["JP"]`), "wuiMdnsAdvertisement": []byte(`true`), "operTweeter": []byte(`true`), "schedulerStartCommand": []byte(`"hook"`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(warnings, "\n")
	for _, want := range []string{"listeners were merged", "TLS settings", "wuiXFF", "wuiAllowCountries", "mDNS", "Twitter/Tweeter", "hook commands"} {
		if !strings.Contains(text, want) {
			t.Fatalf("migration warnings missing %q: %s", want, text)
		}
	}
}

func mustReadCLIFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestInitializeStrataCreatesConfigAndDatabase(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"init"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join("data", "config.json"),
		filepath.Join("data", "rules.json"),
		filepath.Join("data", "strata.db"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s was not created: %v", path, err)
		}
	}
	cfg, err := config.Load(filepath.Join("data", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EffectiveMirakurunPath() != "http://127.0.0.1:40772" {
		t.Fatalf("unexpected Mirakurun URL: %s", cfg.EffectiveMirakurunPath())
	}
}

func TestInitializeStrataRejectsLegacyConfig(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("config.json", []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err = Run(context.Background(), []string{"init"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "legacy config.json detected") {
		t.Fatalf("unexpected error: %v", err)
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

func TestInstallerAcceptedWithoutNodeRuntime(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"installer"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "Node.js/npm modules are not required") || !strings.Contains(text, "Automatic Node-era dependency installation is intentionally not performed") {
		t.Fatalf("unexpected installer output: %s", text)
	}
}

func TestServiceInitscriptIncludesRestart(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"service", "operator", "initscript"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"### BEGIN INIT INFO",
		"# Provides:          strata-pvr-operator",
		"# Short-Description: starts the Strata PVR operator",
		"STRATA_PVR_DIR=" + shellQuote(filepath.ToSlash(cwd)),
		"DAEMON=${STRATA_PVR_DIR}/strata-pvr",
		`DAEMON_OPTS="service operator execute"`,
		"NAME=strata-pvr-operator",
		"USER=$USER",
		"PIDFILE=/var/run/strata-pvr-operator.pid",
		"cd $STRATA_PVR_DIR || exit 1",
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

func TestServiceSchedulerInitscript(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"service", "scheduler", "initscript"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"# Provides:          strata-pvr-scheduler",
		"# Short-Description: starts the Strata PVR scheduler",
		`DAEMON_OPTS="service scheduler execute"`,
		"NAME=strata-pvr-scheduler",
		"PIDFILE=/var/run/strata-pvr-scheduler.pid",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("scheduler initscript missing %q: %s", want, text)
		}
	}
}

func TestShellQuote(t *testing.T) {
	got := shellQuote(`/opt/chi na'chu`)
	if got != `'/opt/chi na'"'"'chu'` {
		t.Fatalf("unexpected shell quote: %s", got)
	}
}

func TestCompatWrapperOutputsSafeLauncher(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"compat", "wrapper"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"#!/bin/bash",
		"STRATA_PVR_DIR=" + shellQuote(filepath.ToSlash(cwd)),
		"DAEMON=${STRATA_PVR_DIR}/strata-pvr",
		`cd "$STRATA_PVR_DIR" || exit 1`,
		`exec "$DAEMON" "$@"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("wrapper missing %q: %s", want, text)
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
	installFakeCompatCommand(t, dir, "ffmpeg")
	installFakeCompatCommand(t, dir, "ffprobe")
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
	if err := os.Mkdir("log", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("web", 0o755); err != nil {
		t.Fatal(err)
	}
	writeCompatWebAssets(t, "web")
	files := map[string]string{
		"config.json":         `{"recordedDir":"recorded","mirakurunPath":"` + mirakurun.URL + `"}`,
		"rules.json":          `[]`,
		"config.sample.json":  `{"recordedDir":"recorded"}`,
		"rules.sample.json":   `[]`,
		"data/schedule.json":  `[]`,
		"data/reserves.json":  `[]`,
		"data/recording.json": `[]`,
		"data/recorded.json":  `[]`,
		"web/index.html":      `<html></html>`,
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
		"OK config.sample.json",
		"OK rules.sample.json",
		"OK data directory",
		"OK log directory",
		"OK recordedDir",
		"OK data/schedule.json",
		"OK data/reserves.json",
		"OK data/recording.json",
		"OK data/recorded.json",
		"OK WUI static assets",
		"OK available disk space",
		"OK ffmpeg command",
		"OK ffprobe command",
		"OK Mirakurun services",
		"OK Mirakurun programs",
		"OK Mirakurun tuners",
		"OK Node.js runtime",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compat output missing %q: %s", want, text)
		}
	}

	out.Reset()
	if err := Run(context.Background(), []string{"compat", "doctor"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text = out.String()
	resolvedRecordedDir, err := filepath.Abs("recorded")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"CONFIG mirakurunPath=" + mirakurun.URL,
		"CONFIG recordedDir=recorded",
		"CONFIG recordedDirResolved=" + resolvedRecordedDir,
		"CONFIG wui=0.0.0.0:disabled open=disabled",
		"CONFIG storageLowSpace=3000MB action=remove",
		"STATE scheduleChannels=0",
		"STATE reserves=0",
		"STATE recording=0",
		"STATE recorded=0",
		"NEXT strata-pvr compat backup",
		"NEXT strata-pvr update -s",
		"NEXT strata-pvr reserves",
		"NEXT strata-pvr service wui execute",
		"NEXT strata-pvr service operator execute",
		"WARN strata-pvr binary not found in the current directory",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compat doctor output missing %q: %s", want, text)
		}
	}
}

func TestCompatStateSummaryReportsArrayLengths(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"data/schedule.json":  `[{}, {}]`,
		"data/reserves.json":  `[{}]`,
		"data/recording.json": `[]`,
		"data/recorded.json":  `[{}, {}, {}]`,
	} {
		if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	writeCompatStateSummary(&out)
	text := out.String()
	for _, want := range []string{
		"STATE scheduleChannels=2",
		"STATE reserves=1",
		"STATE recording=0",
		"STATE recorded=3",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("state summary missing %q: %s", want, text)
		}
	}
}

func TestCompatStateWarningsDetectActiveRecording(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "recording.json"), []byte(`[{"id":"rec"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	warnings := compatStateWarnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "active recordings detected: 1") {
		t.Fatalf("active recording warnings = %#v", warnings)
	}
	if err := os.WriteFile(filepath.Join("data", "recording.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if warnings := compatStateWarnings(); len(warnings) != 0 {
		t.Fatalf("idle recording warnings = %#v", warnings)
	}
}

func installFakeCompatCommand(t *testing.T, dir, name string) {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bin, name)
	data := []byte("#!/bin/sh\nexit 0\n")
	if runtime.GOOS == "windows" {
		path += ".bat"
		data = []byte("@echo off\r\nexit /b 0\r\n")
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestCompatConfigSummaryOmitsSecrets(t *testing.T) {
	port := 20772
	cfg := &config.Config{
		MirakurunPath:              "http://mirakurun.example/",
		RecordedDir:                "recorded",
		RecordedFormat:             "<title>.m2ts",
		WUIHost:                    "127.0.0.1",
		WUIPort:                    &port,
		WUIOpenServer:              true,
		WUIOpenPort:                20773,
		WUIUsers:                   []string{"user:secret"},
		StorageLowSpaceThresholdMB: 1024,
		StorageLowSpaceAction:      "stop",
		NormalizationForm:          "NFKC",
	}
	var out bytes.Buffer
	writeCompatConfigSummary(&out, cfg)
	text := out.String()
	resolvedRecordedDir, err := filepath.Abs("recorded")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"CONFIG mirakurunPath=http://mirakurun.example/",
		"CONFIG recordedDir=recorded",
		"CONFIG recordedDirResolved=" + resolvedRecordedDir,
		"CONFIG wui=127.0.0.1:20772 open=auto:20773",
		"CONFIG storageLowSpace=1024MB action=stop",
		"CONFIG normalizationForm=NFKC",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("summary missing %q: %s", want, text)
		}
	}
	for _, secret := range []string{"user:secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("summary leaked %q: %s", secret, text)
		}
	}
}

func TestCompatDoctorWarningsDetectWrapperTarget(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if warnings := compatDoctorWarnings(); len(warnings) != 1 || !strings.Contains(warnings[0], "binary not found") {
		t.Fatalf("missing binary warnings = %#v", warnings)
	}
	name := "strata-pvr"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(name, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if warnings := compatDoctorWarnings(); len(warnings) != 0 {
		t.Fatalf("existing binary warnings = %#v", warnings)
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

func TestCompatCheckRejectsWrongJSONShapes(t *testing.T) {
	dir := t.TempDir()
	mirakurun := newCompatMirakurun(t)
	defer mirakurun.Close()
	installFakeCompatCommand(t, dir, "ffmpeg")
	installFakeCompatCommand(t, dir, "ffprobe")
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"data", "recorded", "log", "web"} {
		if err := os.Mkdir(name, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeCompatWebAssets(t, "web")
	for name, data := range map[string]string{
		"config.json":         `{"recordedDir":"recorded","mirakurunPath":"` + mirakurun.URL + `"}`,
		"rules.json":          `{}`,
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
	if err == nil || !strings.Contains(err.Error(), "compat check failed") {
		t.Fatalf("expected compat failure, got err=%v output=%s", err, out.String())
	}
	if !strings.Contains(out.String(), "NG rules.json") {
		t.Fatalf("compat output missing wrong rules shape failure: %s", out.String())
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
	if !strings.Contains(out.String(), "OK backup: backup"+string(os.PathSeparator)+"strata-pvr-") {
		t.Fatalf("backup output missing success path: %s", out.String())
	}
	for name, want := range files {
		matches, err := filepath.Glob(filepath.Join("backup", "strata-pvr-*", filepath.FromSlash(name)))
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

func TestCompatCheckWarnsAboutPersonalUseDeprecatedFeatures(t *testing.T) {
	dir := t.TempDir()
	mirakurun := newCompatMirakurun(t)
	defer mirakurun.Close()
	installFakeCompatCommand(t, dir, "ffmpeg")
	installFakeCompatCommand(t, dir, "ffprobe")
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
	if err := os.Mkdir("log", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("web", 0o755); err != nil {
		t.Fatal(err)
	}
	writeCompatWebAssets(t, "web")
	for name, data := range map[string]string{
		"config.json":                           `{"recordedDir":"recorded","mirakurunPath":"` + mirakurun.URL + `","wuiUsers":["strata:yoshikawa"],"wuiAllowCountries":["JP"],"wuiOpenServer":true,"wuiMdnsAdvertisement":true,"operTweeter":true,"wuiTlsKeyPath":"server.pfx"}`,
		"rules.json":                            `[]`,
		"config.sample.json":                    `{"recordedDir":"recorded"}`,
		"rules.sample.json":                     `[]`,
		filepath.Join("data", "schedule.json"):  `[]`,
		filepath.Join("data", "reserves.json"):  `[]`,
		filepath.Join("data", "recording.json"): `[]`,
		filepath.Join("data", "recorded.json"):  `[]`,
	} {
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
		"WARN native settings editing",
		"WARN wuiUsers",
		"WARN wuiOpenServer",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compat check warning missing %q: %s", want, text)
		}
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

func writeCompatWebAssets(t *testing.T, root string) {
	t.Helper()
	for _, dir := range []string{"icons", "lib", "locales", "page"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"index.html", legacyAssetName(".js"), legacyAssetName(".css"), "init.js"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func writeNativeWebAssets(t *testing.T, root string) {
	t.Helper()
	for _, file := range []string{"index.html", "app.js", "styles.css"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeWUIStaticAssetsKeepScheduleNavigationRequirements(t *testing.T) {
	index, err := os.ReadFile(filepath.Join("..", "..", "web", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	app, err := os.ReadFile(filepath.Join("..", "..", "web", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	indexText := string(index)
	appText := string(app)
	for _, want := range []string{
		`value="day"`,
		`value="three-days"`,
		`value="all"`,
		`scheduleHiddenChannel`,
	} {
		if !strings.Contains(indexText, want) {
			t.Fatalf("native WUI index missing %q", want)
		}
	}
	for _, want := range []string{
		`hiddenChannelsStorageKey`,
		`"day": 24`,
		`"three-days": 72`,
		`"all": 0`,
		`saveHiddenChannels`,
		`renderScheduleGuide`,
	} {
		if !strings.Contains(appText, want) {
			t.Fatalf("native WUI app missing %q", want)
		}
	}
	for _, old := range []string{
		`scheduleLimit`,
		`表示数`,
	} {
		if strings.Contains(appText, old) || strings.Contains(indexText, old) {
			t.Fatalf("native WUI static assets still contain removed schedule cap %q", old)
		}
	}
}

func TestCompatDiffReportsStateRewriteStatus(t *testing.T) {
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
		"rules.json":                            "[\n  {\n    \"types\": [\n      \"GR\"\n    ]\n  }\n]",
		filepath.Join("data", "schedule.json"):  `[{"type":"GR","channel":"27","name":"Svc","id":"ch","sid":101,"programs":[]}]`,
		filepath.Join("data", "reserves.json"):  `[{"id":"p","start":1,"end":2,"channel":{}}]`,
		filepath.Join("data", "recording.json"): `[{"id":"p","end":2,"start":1,"channel":{}}]`,
		filepath.Join("data", "recorded.json"):  `not-json`,
	}
	for name, data := range files {
		if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"compat", "diff"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"OK rules.json",
		"OK " + filepath.Join("data", "schedule.json"),
		"OK " + filepath.Join("data", "reserves.json"),
		"DIFF " + filepath.Join("data", "recording.json"),
		"INVALID " + filepath.Join("data", "recorded.json"),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compat diff output missing %q: %s", want, text)
		}
	}
}

func TestCompatCheckAcceptsNativeOrLegacyWUIAssets(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("web", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("web", "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateWUIStaticAssets(); err == nil {
		t.Fatal("expected missing WUI asset error")
	}
	writeNativeWebAssets(t, "web")
	if err := validateWUIStaticAssets(); err != nil {
		t.Fatalf("valid native WUI assets rejected: %v", err)
	}
	if err := os.Remove(filepath.Join("web", "app.js")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join("web", "styles.css")); err != nil {
		t.Fatal(err)
	}
	writeCompatWebAssets(t, "web")
	if err := validateWUIStaticAssets(); err != nil {
		t.Fatalf("valid legacy WUI assets rejected: %v", err)
	}
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
	programs := []legacy.Program{
		{
			ID:       "later",
			Title:    "Late",
			Category: "anime",
			Start:    time.Date(2026, 1, 2, 3, 4, 0, 0, time.Local).UnixMilli(),
			End:      time.Date(2026, 1, 2, 3, 34, 0, 0, time.Local).UnixMilli(),
			Seconds:  1800,
			Channel:  legacy.Channel{Type: "GR", Channel: "27", SID: 101},
		},
		{
			ID:               "earlier",
			Title:            "Early",
			Category:         "news",
			Start:            time.Date(2026, 1, 1, 1, 2, 0, 0, time.Local).UnixMilli(),
			End:              time.Date(2026, 1, 1, 1, 32, 0, 0, time.Local).UnixMilli(),
			Seconds:          1800,
			IsManualReserved: true,
			Channel:          legacy.Channel{Type: "BS", Channel: "BS1", SID: 201},
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
	if !strings.Contains(text, "0  earlier") || !strings.Contains(text, "BS:BS1   news   user") {
		t.Fatalf("manual reserve row missing or unsorted: %s", text)
	}
	if !strings.Contains(text, "1  later") || !strings.Contains(text, "GR:27    anime  rule") {
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
	recorded := []legacy.Program{
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
		"action                Program ID  Recorded",
		"exist                 exists",
		"[simulation] removed  missing",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("cleanup output missing %q: %s", want, text)
		}
	}
	var got []legacy.Program
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
	recorded := []legacy.Program{
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
	var backup []legacy.Program
	if err := storage.ReadJSON(backups[0], &backup, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(backup) != 2 {
		t.Fatalf("backup should contain original list: %#v", backup)
	}
	var got []legacy.Program
	if err := storage.ReadJSON(filepath.Join("data", "recorded.json"), &got, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "exists" {
		t.Fatalf("cleanup should remove only missing entry: %#v", got)
	}
}

func TestCleanupRemovesStrataRecordedRowAndPreviewCache(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"init"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join("data", "strata.db")
	existingPath := filepath.Join(dir, "existing.m2ts")
	if err := os.WriteFile(existingPath, []byte("ts"), 0o644); err != nil {
		t.Fatal(err)
	}
	programs := []legacy.Program{
		{ID: "existing", Recorded: filepath.ToSlash(existingPath)},
		{ID: "missing", Recorded: filepath.ToSlash(filepath.Join(dir, "missing.m2ts"))},
	}
	for _, program := range programs {
		if err := programstore.Upsert(context.Background(), databasePath, filepath.Join("data", "recorded.json"), programstore.Recorded, program); err != nil {
			t.Fatal(err)
		}
	}
	cacheDir := filepath.Join("data", ".cache", "previews")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cacheName := "missing-preview.jpg"
	if err := os.WriteFile(filepath.Join(cacheDir, cacheName), []byte("preview"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.StorePreviewCache(context.Background(), db, database.PreviewCacheEntry{
		CacheKey: "missing-key", ProgramID: "missing", SourcePath: programs[1].Recorded, FileName: cacheName,
	})
	db.Close()
	if err != nil {
		t.Fatal(err)
	}

	if err := Run(context.Background(), []string{"cleanup"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	recorded, err := programstore.Read(context.Background(), databasePath, filepath.Join("data", "recorded.json"), programstore.Recorded)
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || recorded[0].ID != "existing" {
		t.Fatalf("SQLite recorded = %#v", recorded)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, cacheName)); !os.IsNotExist(err) {
		t.Fatalf("preview cache file remains: %v", err)
	}
	db, err = database.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := database.FindPreviewCache(context.Background(), db, "missing-key")
	db.Close()
	if err != nil || found {
		t.Fatalf("preview cache metadata found=%v err=%v", found, err)
	}
	if _, err := os.Stat(filepath.Join("data", "recorded.json")); !os.IsNotExist(err) {
		t.Fatalf("compatibility recorded JSON unexpectedly written: %v", err)
	}
}

func TestRulesPrintsLegacyTable(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	rules := []legacy.Rule{
		{
			Types:         []string{"GR"},
			Categories:    []string{"anime"},
			ReserveTitles: []string{"ニュース", "映画"},
			Hour:          &legacy.RangeRule{Start: 1, End: 4},
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
	for _, want := range []string{"#                     0", "types                 GR", "categories            anime", "hour                  1, 4", "reserve_titles        [2]"} {
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
	rules = append(rules, legacy.Rule{Types: []string{"BS"}})
	if err := storage.WriteJSONAtomic("rules.json", rules, false); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"rules"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "#  types") || !strings.Contains(out.String(), "1  BS     -") {
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

func TestReserveChecksScheduleBeforeDuplicateLikeLegacy(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "schedule.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "reserves.json"), []byte(`[{"id":"old","start":1,"end":2,"channel":{}}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(context.Background(), []string{"reserve", "old"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || err.Error() != "見つかりません" {
		t.Fatalf("reserve error = %v", err)
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
	var reserves []legacy.Program
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
	var reserves []legacy.Program
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
	var reserves []legacy.Program
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
	initial := []legacy.Program{
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
	var got []legacy.Program
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
	reserves := []legacy.Program{{ID: "auto", Title: "Auto"}}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "reserves.json"), reserves, false); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"skip", "--id", "auto"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var got []legacy.Program
	if err := storage.ReadJSON(filepath.Join("data", "reserves.json"), &got, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].IsSkip {
		t.Fatalf("skip did not use legacy id option: %#v", got)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"unskip", "-id", "auto"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"isSkip": true`) {
		t.Fatalf("legacy unskip output should show pre-update target: %s", out.String())
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

func TestSearchDateFiltersCrossMonthAndYearBoundaries(t *testing.T) {
	loc := time.FixedZone("JST", 9*60*60)
	now := time.Date(2026, 12, 31, 23, 30, 0, 0, loc)
	todayProgram := legacy.Program{
		ID:    "today",
		Start: time.Date(2026, 12, 31, 20, 0, 0, 0, loc).UnixMilli(),
		End:   time.Date(2026, 12, 31, 21, 0, 0, 0, loc).UnixMilli(),
	}
	tomorrowProgram := legacy.Program{
		ID:    "tomorrow",
		Start: time.Date(2027, 1, 1, 1, 0, 0, 0, loc).UnixMilli(),
		End:   time.Date(2027, 1, 1, 2, 0, 0, 0, loc).UnixMilli(),
	}

	if !searchMatches(searchOptions{today: true}, todayProgram, now) {
		t.Fatal("today filter did not match the same calendar day")
	}
	if searchMatches(searchOptions{today: true}, tomorrowProgram, now) {
		t.Fatal("today filter matched the next year")
	}
	if !searchMatches(searchOptions{tomorrow: true}, tomorrowProgram, now) {
		t.Fatal("tomorrow filter did not match across the year boundary")
	}
	if searchMatches(searchOptions{tomorrow: true}, todayProgram, now) {
		t.Fatal("tomorrow filter matched today")
	}
}

func TestSearchUsesConfigNormalizationForm(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("config.json", []byte(`{"normalizationForm":"NFKC"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	schedule := `[{"type":"GR","channel":"27","name":"svc","id":"s","sid":101,"programs":[{"id":"p1","category":"anime","title":"ＡＢＣ","fullTitle":"ＡＢＣ","start":1893456000000,"end":1893457800000,"seconds":1800,"channel":{"type":"GR","channel":"27","name":"svc","id":"s","sid":101}}]}]`
	if err := os.WriteFile(filepath.Join("data", "schedule.json"), []byte(schedule), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"search", "-title", "ABC"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "p1") {
		t.Fatalf("normalized search output missing p1: %s", out.String())
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
	if err := storage.WriteJSONAtomic(filepath.Join("data", "recording.json"), []legacy.Program{{ID: "auto"}, {ID: "manual", IsManualReserved: true}}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "reserves.json"), []legacy.Program{{ID: "auto"}, {ID: "manual", IsManualReserved: true}}, false); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"stop", "auto"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "stop:") || !strings.Contains(out.String(), `"abort": true`) {
		t.Fatalf("unexpected stop output: %s", out.String())
	}
	var recording []legacy.Program
	if err := storage.ReadJSON(filepath.Join("data", "recording.json"), &recording, "[]"); err != nil {
		t.Fatal(err)
	}
	if !recording[0].Abort {
		t.Fatalf("recording abort was not set: %#v", recording)
	}
	var reserves []legacy.Program
	if err := storage.ReadJSON(filepath.Join("data", "reserves.json"), &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if !reserves[0].IsSkip || reserves[1].IsSkip {
		t.Fatalf("auto reserve skip was not updated correctly: %#v", reserves)
	}
}

func TestStopUsesStrataDatabaseWithoutRewritingJSON(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"init"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join("data", "strata.db")
	recording := legacy.Program{ID: "auto", Title: "Database recording"}
	if err := programstore.Upsert(context.Background(), databasePath, filepath.Join("data", "recording.json"), programstore.Recording, recording); err != nil {
		t.Fatal(err)
	}
	if err := reservationstore.Upsert(context.Background(), databasePath, filepath.Join("data", "reserves.json"), legacy.Program{ID: "auto"}); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"stop", "auto"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	recordings, err := programstore.Read(context.Background(), databasePath, filepath.Join("data", "recording.json"), programstore.Recording)
	if err != nil {
		t.Fatal(err)
	}
	reserves, err := reservationstore.Read(context.Background(), databasePath, filepath.Join("data", "reserves.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(recordings) != 1 || !recordings[0].Abort || len(reserves) != 1 || !reserves[0].IsSkip {
		t.Fatalf("recordings=%#v reserves=%#v", recordings, reserves)
	}
	for _, path := range []string{filepath.Join("data", "recording.json"), filepath.Join("data", "reserves.json")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("compatibility JSON %s unexpectedly written: %v", path, err)
		}
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
	recording := []legacy.Program{{ID: "rec", Title: "Recording"}}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "recording.json"), recording, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "reserves.json"), []legacy.Program{{ID: "rec"}}, false); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"stop", "--id=rec"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var got []legacy.Program
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
	if err := storage.WriteJSONAtomic(filepath.Join("data", "recording.json"), []legacy.Program{{ID: "auto"}}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "reserves.json"), []legacy.Program{{ID: "auto"}}, false); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"stop", "auto", "--simulation"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "[simulation] stop:") || !strings.Contains(out.String(), `"abort": true`) {
		t.Fatalf("unexpected stop simulation output: %s", out.String())
	}
	var recording []legacy.Program
	if err := storage.ReadJSON(filepath.Join("data", "recording.json"), &recording, "[]"); err != nil {
		t.Fatal(err)
	}
	if recording[0].Abort {
		t.Fatalf("simulation mutated recording: %#v", recording)
	}
	var reserves []legacy.Program
	if err := storage.ReadJSON(filepath.Join("data", "reserves.json"), &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if reserves[0].IsSkip {
		t.Fatalf("simulation mutated reserves: %#v", reserves)
	}
}

func TestCleanupDoesNotRewriteWhenNothingRemoved(t *testing.T) {
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
	recordedPath := filepath.Join("data", "recorded.json")
	original := `[{"id":"exists","recorded":"` + filepath.ToSlash(existing) + `"}]`
	if err := os.WriteFile(recordedPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(recordedPath)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"cleanup"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(recordedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !afterInfo.ModTime().Equal(info.ModTime()) {
		t.Fatalf("cleanup rewrote recorded.json without removals: before=%s after=%s", info.ModTime(), afterInfo.ModTime())
	}
	after, err := os.ReadFile(recordedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatalf("recorded.json changed without removals: %s", string(after))
	}
	backups, err := filepath.Glob(filepath.Join("data", "recorded.json.bak-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("cleanup created backups without removals: %#v", backups)
	}
}
