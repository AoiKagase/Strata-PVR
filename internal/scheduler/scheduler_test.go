package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"strata-pvr/internal/config"
	"strata-pvr/internal/legacy"
	"strata-pvr/internal/mirakurun"
	"strata-pvr/internal/reservationstore"
	"strata-pvr/internal/rulestore"
	"strata-pvr/internal/schedulestore"
	"strata-pvr/internal/storage"
)

type fakeSource struct {
	services    []mirakurun.Service
	programs    []mirakurun.Program
	tuners      []mirakurun.Tuner
	servicesErr error
	programsErr error
	tunersErr   error
}

func runWithSourceTest(t *testing.T, ctx context.Context, paths Paths, cfg *config.Config, source Source, simulation bool, now time.Time) (Result, error) {
	t.Helper()
	legacyFixture := paths.Database == ""
	if paths.Database == "" {
		paths.Database = filepath.Join(filepath.Dir(paths.Schedule), "strata.db")
		var rules []legacy.Rule
		_ = storage.ReadJSON(paths.Rules, &rules, "[]")
		if err := rulestore.Write(ctx, paths.Database, paths.Rules, rules); err != nil {
			t.Fatal(err)
		}
		var schedule []legacy.ChannelSchedule
		_ = storage.ReadJSON(paths.Schedule, &schedule, "[]")
		if err := schedulestore.Write(ctx, paths.Database, schedule); err != nil {
			t.Fatal(err)
		}
		var reserves []legacy.Program
		_ = storage.ReadJSON(paths.Reserves, &reserves, "[]")
		if err := reservationstore.Write(ctx, paths.Database, paths.Reserves, reserves); err != nil {
			t.Fatal(err)
		}
	}
	result, err := RunWithSource(ctx, paths, cfg, source, simulation, now)
	if legacyFixture {
		if schedule, readErr := schedulestore.Read(ctx, paths.Database); readErr == nil {
			_ = storage.WriteJSONAtomic(paths.Schedule, schedule, false)
		}
		if reserves, readErr := reservationstore.Read(ctx, paths.Database, paths.Reserves); readErr == nil {
			_ = storage.WriteJSONAtomic(paths.Reserves, reserves, false)
		}
	}
	return result, err
}

func (f fakeSource) Services(context.Context) ([]mirakurun.Service, error) {
	return f.services, f.servicesErr
}
func (f fakeSource) Programs(context.Context) ([]mirakurun.Program, error) {
	return f.programs, f.programsErr
}
func (f fakeSource) Tuners(context.Context) ([]mirakurun.Tuner, error) {
	return f.tuners, f.tunersErr
}

func loadFixture[T any](t *testing.T, path string) T {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", path))
	if err != nil {
		t.Fatal(err)
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestBuildReservesPreservesManualAndSkip(t *testing.T) {
	now := time.Date(2024, 7, 1, 20, 0, 0, 0, time.Local)
	schedule := []legacy.ChannelSchedule{{
		Channel: legacy.Channel{Type: "GR", Channel: "27", ID: "svc", SID: 101},
		Programs: []legacy.Program{
			{ID: "auto", FullTitle: "Anime", Title: "Anime", Category: "anime", Start: now.Add(time.Hour).UnixMilli(), End: now.Add(2 * time.Hour).UnixMilli(), Seconds: 3600, Channel: legacy.Channel{Type: "GR", Channel: "27", ID: "svc", SID: 101}},
			{ID: "manual", FullTitle: "News", Title: "News", Category: "news", Start: now.Add(3 * time.Hour).UnixMilli(), End: now.Add(4 * time.Hour).UnixMilli(), Seconds: 3600, Channel: legacy.Channel{Type: "GR", Channel: "27", ID: "svc", SID: 101}},
		},
	}}
	rules := []legacy.Rule{{Categories: []string{"anime"}, RecordedFormat: "<title>.m2ts"}}
	old := []legacy.Program{
		{ID: "auto", IsSkip: true},
		{ID: "manual", IsManualReserved: true, OneSeg: true, Start: now.Add(3 * time.Hour).UnixMilli(), End: now.Add(4 * time.Hour).UnixMilli()},
	}
	reserves, result := BuildReserves(schedule, rules, old, []mirakurun.Tuner{{Types: []string{"GR"}}}, now)
	if len(reserves) != 2 {
		t.Fatalf("len(reserves) = %d", len(reserves))
	}
	if !reserves[0].IsSkip {
		t.Fatal("auto reserve skip flag was not preserved")
	}
	if reserves[0].RecordedFormat != "<title>.m2ts" {
		t.Fatalf("recorded format = %q", reserves[0].RecordedFormat)
	}
	if !reserves[1].IsManualReserved || !reserves[1].OneSeg {
		t.Fatalf("manual reserve was not preserved: %#v", reserves[1])
	}
	if result.Skips != 1 || result.Reserves != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestBuildReservesUsesNormalizationForm(t *testing.T) {
	now := time.Date(2024, 7, 1, 20, 0, 0, 0, time.Local)
	schedule := []legacy.ChannelSchedule{{
		Channel: legacy.Channel{Type: "GR", Channel: "27", ID: "svc", SID: 101},
		Programs: []legacy.Program{
			{ID: "norm", FullTitle: "ＡＢＣ特集", Title: "ＡＢＣ", Detail: "第１話", Category: "anime", Start: now.Add(time.Hour).UnixMilli(), End: now.Add(2 * time.Hour).UnixMilli(), Seconds: 3600, Channel: legacy.Channel{Type: "GR", Channel: "27", ID: "svc", SID: 101}},
		},
	}}
	rules := []legacy.Rule{{ReserveTitles: []string{"ABC特集"}, ReserveDescriptions: []string{"第1話"}, RecordedFormat: "<title>.m2ts"}}
	without, _ := BuildReserves(schedule, rules, nil, []mirakurun.Tuner{{Types: []string{"GR"}}}, now)
	if len(without) != 0 {
		t.Fatalf("unexpected unnormalized reserves: %#v", without)
	}
	reserves, result := BuildReservesWithNormalization(schedule, rules, nil, []mirakurun.Tuner{{Types: []string{"GR"}}}, now, "NFKC")
	if len(reserves) != 1 || reserves[0].ID != "norm" {
		t.Fatalf("normalized reserves = %#v", reserves)
	}
	if reserves[0].RecordedFormat != "<title>.m2ts" {
		t.Fatalf("recorded format = %q", reserves[0].RecordedFormat)
	}
	if result.Matches != 1 || result.Reserves != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunWithSourceWritesScheduleAndReserves(t *testing.T) {
	dir := t.TempDir()
	paths := Paths{
		Config:   filepath.Join(dir, "config.json"),
		Rules:    filepath.Join(dir, "rules.json"),
		Schedule: filepath.Join(dir, "data", "schedule.json"),
		Reserves: filepath.Join(dir, "data", "reserves.json"),
		Log:      filepath.Join(dir, "log", "scheduler"),
	}
	if err := os.WriteFile(paths.Rules, []byte(`[{"reserve_titles":["Title"]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Reserves, []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	src := fakeSource{
		services: []mirakurun.Service{{ID: 1, ServiceID: 101, NetworkID: 1, Name: "svc"}},
		programs: []mirakurun.Program{{ID: 10, NetworkID: 1, ServiceID: 101, Name: "Title", StartAt: time.Now().Add(time.Hour).UnixMilli(), Duration: 3600000}},
		tuners:   []mirakurun.Tuner{{Types: []string{"GR"}}},
	}
	src.services[0].Channel.Type = "GR"
	src.services[0].Channel.Channel = "27"
	_, err := runWithSourceTest(t, context.Background(), paths, &config.Config{}, src, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.Schedule); err != nil {
		t.Fatal(err)
	}
	logData, err := os.ReadFile(paths.Log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "RUNNING SCHEDULER.") || !strings.Contains(string(logData), "RESERVE:") || !strings.Contains(string(logData), "MATCHES: 1") || !strings.Contains(string(logData), "RESERVES: 1") {
		t.Fatalf("scheduler log missing expected lines: %s", string(logData))
	}
	for _, want := range []string{
		"GETTING EPG from Mirakurun.",
		"Mirakurun is OK.",
		"Mirakurun -> services: 1",
		"Mirakurun -> services: 1 (excluded)",
		"Mirakurun -> sorted services: 0",
		"Mirakurun -> programs: 1",
		"Mirakurun -> tuners: 1",
	} {
		if !strings.Contains(string(logData), want) {
			t.Fatalf("scheduler log missing %q: %s", want, string(logData))
		}
	}
	if !strings.Contains(string(logData), "WRITE: "+paths.Schedule) || !strings.Contains(string(logData), "WRITE: "+paths.Reserves) {
		t.Fatalf("scheduler log missing write lines: %s", string(logData))
	}
	if !strings.Contains(string(logData), `TUNERS: {"GR":1}`) {
		t.Fatalf("scheduler log missing legacy tuner type counts: %s", string(logData))
	}
}

func TestRunWithSourceWritesSchedulerStateToStrataDatabase(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	paths := Paths{
		Database: filepath.Join(dir, "data", "strata.db"),
		Rules:    filepath.Join(dir, "data", "rules.json"), Schedule: filepath.Join(dir, "data", "schedule.json"),
		Reserves: filepath.Join(dir, "data", "reserves.json"), Log: filepath.Join(dir, "log", "scheduler"),
	}
	if err := os.MkdirAll(filepath.Dir(paths.Database), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := rulestore.Write(context.Background(), paths.Database, paths.Rules, []legacy.Rule{{ReserveTitles: []string{"Database Title"}}}); err != nil {
		t.Fatal(err)
	}
	src := fakeSource{
		services: []mirakurun.Service{{ID: 1, ServiceID: 101, NetworkID: 1, Name: "svc"}},
		programs: []mirakurun.Program{{ID: 10, NetworkID: 1, ServiceID: 101, Name: "Database Title", StartAt: now.Add(time.Hour).UnixMilli(), Duration: 3600000}},
		tuners:   []mirakurun.Tuner{{Types: []string{"GR"}}},
	}
	src.services[0].Channel.Type = "GR"
	src.services[0].Channel.Channel = "27"
	if _, err := runWithSourceTest(t, context.Background(), paths, &config.Config{}, src, false, now); err != nil {
		t.Fatal(err)
	}
	schedule, err := schedulestore.Read(context.Background(), paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if len(schedule) != 1 || len(schedule[0].Programs) != 1 || schedule[0].Programs[0].Title != "Database Title" {
		t.Fatalf("SQLite schedule = %#v", schedule)
	}
	reserves, err := reservationstore.Read(context.Background(), paths.Database, paths.Reserves)
	if err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 1 || reserves[0].ID != schedule[0].Programs[0].ID {
		t.Fatalf("SQLite reservations = %#v", reserves)
	}
	if _, err := os.Stat(paths.Schedule); !os.IsNotExist(err) {
		t.Fatalf("compatibility schedule JSON unexpectedly written: %v", err)
	}
}

func TestRunWithSourceUsesMirakurunFixtures(t *testing.T) {
	dir := t.TempDir()
	paths := Paths{
		Rules:    filepath.Join(dir, "rules.json"),
		Schedule: filepath.Join(dir, "data", "schedule.json"),
		Reserves: filepath.Join(dir, "data", "reserves.json"),
		Log:      filepath.Join(dir, "log", "scheduler"),
	}
	if err := os.Mkdir(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Rules, []byte(`[{"reserve_titles":["Fixture Anime"]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Reserves, []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2024, 7, 3, 20, 0, 0, 0, time.Local)
	programs := loadFixture[[]mirakurun.Program](t, filepath.Join("mirakurun", "programs.json"))
	programs[0].StartAt = now.Add(time.Hour).UnixMilli()
	src := fakeSource{
		services: loadFixture[[]mirakurun.Service](t, filepath.Join("mirakurun", "services.json")),
		programs: programs,
		tuners:   loadFixture[[]mirakurun.Tuner](t, filepath.Join("mirakurun", "tuners.json")),
	}

	result, err := runWithSourceTest(t, context.Background(), paths, &config.Config{}, src, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Matches != 1 || result.Reserves != 1 {
		t.Fatalf("result = %#v", result)
	}
	var reserves []legacy.Program
	if data, err := os.ReadFile(paths.Reserves); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal(data, &reserves); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 1 || reserves[0].Title != "Fixture Anime" || reserves[0].Channel.Name != "Fixture GR" {
		t.Fatalf("unexpected reserves from fixtures: %#v", reserves)
	}
}

func TestRunWithSourceLogsMirakurunErrorDetails(t *testing.T) {
	dir := t.TempDir()
	paths := Paths{
		Rules:    filepath.Join(dir, "rules.json"),
		Schedule: filepath.Join(dir, "data", "schedule.json"),
		Reserves: filepath.Join(dir, "data", "reserves.json"),
		Log:      filepath.Join(dir, "log", "scheduler"),
	}
	if err := os.Mkdir(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Rules, []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Reserves, []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runWithSourceTest(t, context.Background(), paths, &config.Config{}, fakeSource{programsErr: fmt.Errorf("program endpoint down")}, true, time.Now())
	if err == nil || !strings.Contains(err.Error(), "get Mirakurun programs") {
		t.Fatalf("expected program fetch error, got %v", err)
	}
	logData, readErr := os.ReadFile(paths.Log)
	if readErr != nil {
		t.Fatal(readErr)
	}
	logText := string(logData)
	if !strings.Contains(logText, "Mirakurun -> Error:") || !strings.Contains(logText, "program endpoint down") {
		t.Fatalf("scheduler log missing Mirakurun error details: %s", logText)
	}
}

func TestRunWithSourceLogsDuplicateProgramIDWarning(t *testing.T) {
	dir := t.TempDir()
	paths := Paths{
		Rules:    filepath.Join(dir, "rules.json"),
		Schedule: filepath.Join(dir, "data", "schedule.json"),
		Reserves: filepath.Join(dir, "data", "reserves.json"),
		Log:      filepath.Join(dir, "log", "scheduler"),
	}
	if err := os.Mkdir(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Rules, []byte(`[{"reserve_titles":["Title"]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Reserves, []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(time.Hour).UnixMilli()
	src := fakeSource{
		services: []mirakurun.Service{
			{ID: 1, ServiceID: 101, NetworkID: 1, Name: "svc1"},
			{ID: 2, ServiceID: 102, NetworkID: 1, Name: "svc2"},
		},
		programs: []mirakurun.Program{
			{ID: 10, NetworkID: 1, ServiceID: 101, Name: "Title A", StartAt: start, Duration: 3600000},
			{ID: 10, NetworkID: 1, ServiceID: 102, Name: "Title B", StartAt: start + 3600000, Duration: 3600000},
		},
		tuners: []mirakurun.Tuner{{Types: []string{"GR", "BS"}}, {Types: []string{"GR"}}},
	}
	src.services[0].Channel.Type = "GR"
	src.services[0].Channel.Channel = "27"
	src.services[1].Channel.Type = "GR"
	src.services[1].Channel.Channel = "28"

	_, err := runWithSourceTest(t, context.Background(), paths, &config.Config{}, src, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	logData, err := os.ReadFile(paths.Log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "**WARNING**: a is duplicated!") {
		t.Fatalf("scheduler log missing duplicate id warning: %s", string(logData))
	}
	if !strings.Contains(string(logData), `TUNERS: {"GR":2,"BS":1}`) {
		t.Fatalf("scheduler log missing ordered tuner type counts: %s", string(logData))
	}
}

func TestRunWithSourceLogsLegacyDuplicateReservation(t *testing.T) {
	dir := t.TempDir()
	paths := Paths{
		Rules:    filepath.Join(dir, "rules.json"),
		Schedule: filepath.Join(dir, "data", "schedule.json"),
		Reserves: filepath.Join(dir, "data", "reserves.json"),
		Log:      filepath.Join(dir, "log", "scheduler"),
	}
	if err := os.Mkdir(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Rules, []byte(`[{"reserve_titles":["Same Title"]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Reserves, []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(time.Hour).UnixMilli()
	src := fakeSource{
		services: []mirakurun.Service{
			{ID: 1, ServiceID: 101, NetworkID: 1, Name: "svc-low"},
			{ID: 2, ServiceID: 102, NetworkID: 1, Name: "svc-high"},
		},
		programs: []mirakurun.Program{
			{ID: 10, NetworkID: 1, ServiceID: 101, Name: "Same Title", StartAt: start, Duration: 3600000},
			{ID: 11, NetworkID: 1, ServiceID: 102, Name: "Same Title", StartAt: start, Duration: 3600000},
		},
		tuners: []mirakurun.Tuner{{Types: []string{"GR"}}},
	}
	src.services[0].Channel.Type = "GR"
	src.services[0].Channel.Channel = "27"
	src.services[1].Channel.Type = "GR"
	src.services[1].Channel.Channel = "27"

	result, err := runWithSourceTest(t, context.Background(), paths, &config.Config{}, src, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Duplicates != 1 {
		t.Fatalf("duplicates = %d", result.Duplicates)
	}
	logData, err := os.ReadFile(paths.Log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "DUPLICATE: b ") || !strings.Contains(string(logData), "[svc-high] Same Title") {
		t.Fatalf("scheduler log missing legacy duplicate line: %s", string(logData))
	}
}

func TestRunWithSourceLogsManualReserveOverriddenByRule(t *testing.T) {
	dir := t.TempDir()
	paths := Paths{
		Rules:    filepath.Join(dir, "rules.json"),
		Schedule: filepath.Join(dir, "data", "schedule.json"),
		Reserves: filepath.Join(dir, "data", "reserves.json"),
		Log:      filepath.Join(dir, "log", "scheduler"),
	}
	if err := os.Mkdir(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Rules, []byte(`[{"reserve_titles":["Rule Match"]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(time.Hour).UnixMilli()
	oldReserve := `[{"id":"a","title":"Manual Title","start":` + strconv.FormatInt(start, 10) + `,"end":` + strconv.FormatInt(start+3600000, 10) + `,"isManualReserved":true,"channel":{"name":"manual-svc"}}]`
	if err := os.WriteFile(paths.Reserves, []byte(oldReserve), 0o644); err != nil {
		t.Fatal(err)
	}
	src := fakeSource{
		services: []mirakurun.Service{{ID: 1, ServiceID: 101, NetworkID: 1, Name: "svc"}},
		programs: []mirakurun.Program{
			{ID: 10, NetworkID: 1, ServiceID: 101, Name: "Rule Match", StartAt: start, Duration: 3600000},
		},
		tuners: []mirakurun.Tuner{{Types: []string{"GR"}}},
	}
	src.services[0].Channel.Type = "GR"
	src.services[0].Channel.Channel = "27"

	_, err := runWithSourceTest(t, context.Background(), paths, &config.Config{}, src, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	logData, err := os.ReadFile(paths.Log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "OVERRIDEBYRULE: a ") || !strings.Contains(string(logData), "[manual-svc] Manual Title") {
		t.Fatalf("scheduler log missing manual override line: %s", string(logData))
	}
}

func TestRunWithSourceLogsLegacyConflictPrefix(t *testing.T) {
	dir := t.TempDir()
	paths := Paths{
		Rules:    filepath.Join(dir, "rules.json"),
		Schedule: filepath.Join(dir, "data", "schedule.json"),
		Reserves: filepath.Join(dir, "data", "reserves.json"),
		Log:      filepath.Join(dir, "log", "scheduler"),
	}
	if err := os.Mkdir(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Rules, []byte(`[{"reserve_titles":["Title"]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Reserves, []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(time.Hour).UnixMilli()
	src := fakeSource{
		services: []mirakurun.Service{{ID: 1, ServiceID: 101, NetworkID: 1, Name: "svc"}},
		programs: []mirakurun.Program{
			{ID: 10, NetworkID: 1, ServiceID: 101, Name: "Title A", StartAt: start, Duration: 3600000},
			{ID: 11, NetworkID: 1, ServiceID: 101, Name: "Title B", StartAt: start, Duration: 3600000},
		},
		tuners: []mirakurun.Tuner{{Types: []string{"GR"}}},
	}
	src.services[0].Channel.Type = "GR"
	src.services[0].Channel.Channel = "27"
	result, err := runWithSourceTest(t, context.Background(), paths, &config.Config{}, src, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if result.Conflicts != 1 {
		t.Fatalf("conflicts = %d", result.Conflicts)
	}
	logData, err := os.ReadFile(paths.Log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "!CONFLICT:") {
		t.Fatalf("scheduler log missing legacy conflict prefix: %s", string(logData))
	}
	if strings.Contains(string(logData), "+00:00") || strings.Contains(string(logData), "+09:00") {
		t.Fatalf("scheduler log used RFC3339 timezone instead of legacy isoDateTime: %s", string(logData))
	}
}

func TestLegacyISODateTimeUsesDateformatOffset(t *testing.T) {
	oldLocal := time.Local
	time.Local = time.FixedZone("JST", 9*60*60)
	defer func() { time.Local = oldLocal }()

	ts := time.Date(2024, 7, 1, 20, 30, 45, 0, time.Local).UnixMilli()
	if got := legacyISODateTime(ts); got != "2024-07-01T20:30:45+0900" {
		t.Fatalf("legacyISODateTime = %q", got)
	}
}

func TestPIDFileLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "scheduler.pid")
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
