package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	passwordauth "strata-pvr/internal/auth"
	"strata-pvr/internal/config"
	"strata-pvr/internal/database"
	legacy "strata-pvr/internal/domain"
	"strata-pvr/internal/programstore"
	"strata-pvr/internal/reservationstore"
	"strata-pvr/internal/rulestore"
	"strata-pvr/internal/schedulestore"
	"strata-pvr/internal/storage"
)

func TestMain(m *testing.M) {
	resolveRuntimePaths = func() paths {
		if _, err := os.Stat(filepath.Join("data", "config.json")); err == nil {
			return runtimePaths()
		}
		p := paths{
			config:   "config.json",
			database: filepath.Join("data", "strata.db"),
		}
		return p
	}
	validateRuntimePaths = func(p paths) error {
		seedCLITestDatabase(p)
		return nil
	}
	os.Exit(m.Run())
}

func seedCLITestDatabase(p paths) {
	if _, err := os.Stat(p.database); err == nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p.database), 0o755)
	ctx := context.Background()
	var rules []legacy.Rule
	if storage.ReadJSON("rules.json", &rules, "[]") == nil {
		_ = rulestore.Write(ctx, p.database, rules)
	}
	var schedule []legacy.ChannelSchedule
	if storage.ReadJSON(filepath.Join("data", "schedule.json"), &schedule, "[]") == nil {
		_ = schedulestore.Write(ctx, p.database, schedule)
	}
	var reserves []legacy.Program
	if storage.ReadJSON(filepath.Join("data", "reserves.json"), &reserves, "[]") == nil {
		_ = reservationstore.Write(ctx, p.database, reserves)
	}
	for _, item := range []struct{ path, collection string }{{filepath.Join("data", "recording.json"), programstore.Recording}, {filepath.Join("data", "recorded.json"), programstore.Recorded}} {
		var programs []legacy.Program
		if storage.ReadJSON(item.path, &programs, "[]") == nil {
			_ = programstore.Write(ctx, p.database, item.collection, programs)
		}
	}
}

func TestRequireStrataRuntimeRejectsLegacyRootConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"mirakurunPath":"http://127.0.0.1:40772"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := requireStrataRuntime(paths{
		config:   filepath.Join(dir, "data", "config.json"),
		database: filepath.Join(dir, "data", "strata.db"),
	})
	if err == nil || !strings.Contains(err.Error(), "run strata-pvr init or migrate") {
		t.Fatalf("unexpected runtime validation error: %v", err)
	}
}

func TestRuntimePathsUseOnlyNativeData(t *testing.T) {
	p := runtimePaths()
	if p.config != filepath.Join("data", "config.json") || p.database != filepath.Join("data", "strata.db") {
		t.Fatalf("unexpected runtime paths: %#v", p)
	}
	if !isRuntimeCommand([]string{"reserves"}) || !isRuntimeCommand([]string{"service", "wui", "execute"}) || !isRuntimeCommand([]string{"run", "wui"}) {
		t.Fatal("operational commands were not classified as runtime commands")
	}
	if isRuntimeCommand([]string{"service", "wui", "initscript"}) {
		t.Fatal("non-runtime commands require initialized data")
	}
}

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
	if got := string(mustReadCLIFile(t, filepath.Join("migrate", "config.json"))); got != legacyConfig {
		t.Fatalf("migrate input was modified: %q", got)
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
	if err := Run(context.Background(), []string{"migrate"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("migration accepted invalid config")
	}
	if _, err := os.Stat(filepath.Join("migrate", "config.json")); err != nil {
		t.Fatal("migration input was modified")
	}
	if _, err := os.Stat("data"); !os.IsNotExist(err) {
		t.Fatalf("partial data directory exists: %v", err)
	}
}

func TestMigrateArchiveMismatchRollsBackAndCanRetry(t *testing.T) {
	withMigrationTestDir(t)
	writeMinimalMigrationInput(t)
	originalInspect := inspectArchivedMigrationFiles
	inspectArchivedMigrationFiles = func(root string) (map[string]string, map[string]int64, error) {
		hashes, sizes, err := inspectMigrationFiles(root)
		if err == nil {
			hashes["config.json"] = strings.Repeat("0", 64)
		}
		return hashes, sizes, err
	}
	err := migrateChinachu(context.Background(), nil, &bytes.Buffer{})
	inspectArchivedMigrationFiles = originalInspect
	if err == nil || !strings.Contains(err.Error(), "source files changed") {
		t.Fatalf("unexpected archive verification error: %v", err)
	}
	if _, err := os.Stat(filepath.Join("migrate", "config.json")); err != nil {
		t.Fatalf("migration input was not restored: %v", err)
	}
	if _, err := os.Stat("data"); !os.IsNotExist(err) {
		t.Fatalf("failed migration installed data: %v", err)
	}
	if err := migrateChinachu(context.Background(), nil, &bytes.Buffer{}); err != nil {
		t.Fatalf("migration retry failed: %v", err)
	}
}

func TestMigrateArchiveCopyFailureLeavesInputUntouched(t *testing.T) {
	withMigrationTestDir(t)
	writeMinimalMigrationInput(t)
	originalCopy := copyMigrationInput
	copyMigrationInput = func(oldPath, newPath string) error {
		return fmt.Errorf("injected archive copy failure")
	}
	err := migrateChinachu(context.Background(), nil, &bytes.Buffer{})
	copyMigrationInput = originalCopy
	if err == nil || !strings.Contains(err.Error(), "archive migration input") {
		t.Fatalf("unexpected archive move error: %v", err)
	}
	if _, err := os.Stat(filepath.Join("migrate", "config.json")); err != nil {
		t.Fatalf("migration input changed after archive failure: %v", err)
	}
	if _, err := os.Stat("data"); !os.IsNotExist(err) {
		t.Fatalf("archive failure installed data: %v", err)
	}
}

func withMigrationTestDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

func writeMinimalMigrationInput(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join("migrate", "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join("migrate", "config.json"):            `{"mirakurunPath":"http://127.0.0.1:40772","recordedDir":"./recorded/","recordedFormat":"<title>.m2ts","wuiOpenServer":true,"wuiOpenPort":20772}`,
		filepath.Join("migrate", "rules.json"):             `[]`,
		filepath.Join("migrate", "data", "reserves.json"):  `[]`,
		filepath.Join("migrate", "data", "recording.json"): `[]`,
		filepath.Join("migrate", "data", "recorded.json"):  `[]`,
		filepath.Join("migrate", "data", "schedule.json"):  `[]`,
	}
	for path, data := range files {
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
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

func TestConvertLegacyConfigWarnsWhenListenersAreMerged(t *testing.T) {
	port := 20772
	_, warnings, err := convertLegacyConfig(&config.LegacyConfig{
		WUIPort: &port, WUIHost: "127.0.0.1", WUIOpenServer: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(warnings, "\n")
	if !strings.Contains(text, "listeners were merged") {
		t.Fatalf("migration warning missing listener merge: %s", text)
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
		`DAEMON_OPTS="run operator"`,
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
		`DAEMON_OPTS="run scheduler"`,
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

func TestPrepareServiceRuntimeRequiresInitialization(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := prepareServiceRuntimeFor(runtimePaths()); err == nil || !strings.Contains(err.Error(), "init or migrate") {
		t.Fatalf("unexpected preparation error: %v", err)
	}
}

func TestPrepareServiceRuntimeCreatesRuntimeDirectories(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "config.json"), config.DefaultDocument(), true); err != nil {
		t.Fatal(err)
	}
	if err := prepareServiceRuntimeFor(runtimePaths()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("log"); err != nil {
		t.Fatalf("log directory was not prepared: %v", err)
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

func TestProgramListPrintsTabSeparatedColumns(t *testing.T) {
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
		"action\tProgram ID\tRecorded",
		"exist\texists",
		"[simulation] removed\tmissing",
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
		if err := programstore.Upsert(context.Background(), databasePath, programstore.Recorded, program); err != nil {
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
	recorded, err := programstore.Read(context.Background(), databasePath, programstore.Recorded)
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

func TestRulesPrintsTabSeparatedTable(t *testing.T) {
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
	rules = append(rules, legacy.Rule{Types: []string{"BS"}})
	if err := rulestore.Write(context.Background(), filepath.Join("data", "strata.db"), rules); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Run(context.Background(), []string{"rules"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "#\ttypes") || !strings.Contains(out.String(), "1\tBS\t-") {
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
	reserves, err := reservationstore.Read(context.Background(), filepath.Join("data", "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 1 || !reserves[0].IsManualReserved {
		t.Fatalf("reserve was not stored: %#v", reserves)
	}
	if !reserves[0].OneSeg {
		t.Fatalf("1seg flag not stored: %#v", reserves[0])
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
	rules, err := rulestore.Read(context.Background(), filepath.Join("data", "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || len(rules[0].Types) != 1 || rules[0].Types[0] != "GR" || len(rules[0].ReserveTitles) != 1 || rules[0].ReserveTitles[0] != "笑点" {
		t.Fatalf("rule not stored: %#v", rules)
	}
	if err := Run(context.Background(), []string{"disrule", "0"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	rules, _ = rulestore.Read(context.Background(), filepath.Join("data", "strata.db"))
	if len(rules) != 1 || !rules[0].IsDisabled {
		t.Fatalf("rule not disabled: %#v", rules)
	}
	if err := Run(context.Background(), []string{"rmrule", "0"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	rules, _ = rulestore.Read(context.Background(), filepath.Join("data", "strata.db"))
	if len(rules) != 0 {
		t.Fatalf("rule not removed: %#v", rules)
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
	rules, err := rulestore.Read(context.Background(), filepath.Join("data", "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || len(rules[0].ReserveTitles) != 0 || rules[0].Hour != nil || rules[0].Duration != nil {
		t.Fatalf("rule deletion markers were not applied: %#v", rules)
	}
	if len(rules[0].Types) != 1 || rules[0].Types[0] != "GR" {
		t.Fatalf("remaining rule condition was lost: %#v", rules[0])
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
	doc := config.DefaultDocument()
	doc.Advanced.NormalizationForm = "NFKC"
	if err := storage.WriteJSONAtomic(filepath.Join("data", "config.json"), doc, true); err != nil {
		t.Fatal(err)
	}
	schedule := `[{"type":"GR","channel":"27","name":"svc","id":"s","sid":101,"programs":[{"id":"p1","category":"anime","title":"ＡＢＣ","fullTitle":"ＡＢＣ","start":1893456000000,"end":1893457800000,"seconds":1800,"channel":{"type":"GR","channel":"27","name":"svc","id":"s","sid":101}}]}]`
	if err := os.WriteFile(filepath.Join("data", "schedule.json"), []byte(schedule), 0o644); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join("data", "strata.db")
	var schedules []legacy.ChannelSchedule
	if err := storage.ReadJSON(filepath.Join("data", "schedule.json"), &schedules, "[]"); err != nil {
		t.Fatal(err)
	}
	if err := schedulestore.Write(context.Background(), databasePath, schedules); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := search(paths{config: filepath.Join("data", "config.json"), database: databasePath}, []string{"-title", "ABC"}, &out); err != nil {
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
	if values, err := programstore.Read(context.Background(), filepath.Join("data", "strata.db"), programstore.Recording); err != nil {
		t.Fatal(err)
	} else {
		recording = values
	}
	if !recording[0].Abort {
		t.Fatalf("recording abort was not set: %#v", recording)
	}
	var reserves []legacy.Program
	if values, err := reservationstore.Read(context.Background(), filepath.Join("data", "strata.db")); err != nil {
		t.Fatal(err)
	} else {
		reserves = values
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
	if err := programstore.Upsert(context.Background(), databasePath, programstore.Recording, recording); err != nil {
		t.Fatal(err)
	}
	if err := reservationstore.Upsert(context.Background(), databasePath, legacy.Program{ID: "auto"}); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"stop", "auto"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	recordings, err := programstore.Read(context.Background(), databasePath, programstore.Recording)
	if err != nil {
		t.Fatal(err)
	}
	reserves, err := reservationstore.Read(context.Background(), databasePath)
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
