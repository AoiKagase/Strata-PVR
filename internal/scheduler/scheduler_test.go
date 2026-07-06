package scheduler

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"chinachu-go/internal/chinachu"
	"chinachu-go/internal/config"
	"chinachu-go/internal/mirakurun"
)

func TestMain(m *testing.M) {
	if strings.HasPrefix(filepath.Base(os.Args[0]), "scheduler-hook") {
		out := os.Args[0] + ".args"
		f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			os.Exit(2)
		}
		_, _ = fmt.Fprintln(f, strings.Join(os.Args[1:], "\t"))
		_ = f.Close()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fakeSource struct {
	services []mirakurun.Service
	programs []mirakurun.Program
	tuners   []mirakurun.Tuner
}

func (f fakeSource) Services(context.Context) ([]mirakurun.Service, error) { return f.services, nil }
func (f fakeSource) Programs(context.Context) ([]mirakurun.Program, error) { return f.programs, nil }
func (f fakeSource) Tuners(context.Context) ([]mirakurun.Tuner, error)     { return f.tuners, nil }

func TestBuildReservesPreservesManualAndSkip(t *testing.T) {
	now := time.Date(2024, 7, 1, 20, 0, 0, 0, time.Local)
	schedule := []chinachu.ChannelSchedule{{
		Channel: chinachu.Channel{Type: "GR", Channel: "27", ID: "svc", SID: 101},
		Programs: []chinachu.Program{
			{ID: "auto", FullTitle: "Anime", Title: "Anime", Category: "anime", Start: now.Add(time.Hour).UnixMilli(), End: now.Add(2 * time.Hour).UnixMilli(), Seconds: 3600, Channel: chinachu.Channel{Type: "GR", Channel: "27", ID: "svc", SID: 101}},
			{ID: "manual", FullTitle: "News", Title: "News", Category: "news", Start: now.Add(3 * time.Hour).UnixMilli(), End: now.Add(4 * time.Hour).UnixMilli(), Seconds: 3600, Channel: chinachu.Channel{Type: "GR", Channel: "27", ID: "svc", SID: 101}},
		},
	}}
	rules := []chinachu.Rule{{Categories: []string{"anime"}, RecordedFormat: "<title>.m2ts"}}
	old := []chinachu.Program{
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
	_, err := RunWithSource(context.Background(), paths, &config.Config{}, src, false, time.Now())
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

func TestRunWithSourceRunsSchedulerHooks(t *testing.T) {
	dir := t.TempDir()
	hook := copyHookExecutable(t, dir)
	paths := Paths{
		Rules:    filepath.Join(dir, "rules.json"),
		Reserves: filepath.Join(dir, "data", "reserves.json"),
		Schedule: filepath.Join(dir, "data", "schedule.json"),
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
	start := time.Now().Add(time.Hour).UnixMilli()
	src := fakeSource{
		services: []mirakurun.Service{{ID: 1, ServiceID: 101, NetworkID: 1, Name: "svc"}},
		programs: []mirakurun.Program{
			{ID: 1, ServiceID: 101, NetworkID: 1, Name: "Title", StartAt: start, Duration: 3600000},
			{ID: 2, ServiceID: 101, NetworkID: 1, Name: "Title 2", StartAt: start, Duration: 3600000},
		},
		tuners: []mirakurun.Tuner{{Types: []string{"GR"}}},
	}
	src.services[0].Channel.Type = "GR"
	src.services[0].Channel.Channel = "27"

	_, err := RunWithSource(context.Background(), paths, &config.Config{
		EPGStartCommand:       hook,
		EPGEndCommand:         hook,
		SchedulerStartCommand: hook,
		SchedulerEndCommand:   hook,
		ConflictCommand:       hook,
	}, src, false, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(hook + ".args")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 5 {
		t.Fatalf("hook invocations = %d, lines: %q", len(lines), data)
	}
	for i := 0; i < 3; i++ {
		fields := strings.Split(lines[i], "\t")
		if len(fields) != 4 || fields[1] != paths.Rules || fields[2] != paths.Reserves || fields[3] != paths.Schedule {
			t.Fatalf("scheduler hook args[%d] = %q", i, lines[i])
		}
	}
	conflict := strings.Split(lines[3], "\t")
	if len(conflict) != 6 || conflict[1] != "2" || conflict[3] != "svc" || conflict[4] != "Title 2" || !strings.Contains(conflict[5], `"isConflict":true`) {
		t.Fatalf("conflict hook args = %q", lines[3])
	}
	end := strings.Split(lines[4], "\t")
	if len(end) != 9 || end[4] != "2" || end[5] != "0" || end[6] != "1" || end[7] != "0" || end[8] != "1" {
		t.Fatalf("scheduler end args = %q", lines[4])
	}
	logData, err := os.ReadFile(paths.Log)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(logData), "SPAWN: "+hook); got != 5 {
		t.Fatalf("SPAWN log count = %d in %s", got, logData)
	}
}

func copyHookExecutable(t *testing.T, dir string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ext := filepath.Ext(exe)
	if ext == "" {
		ext = ".exe"
	}
	hook := filepath.Join(dir, "scheduler-hook"+ext)
	in, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(hook, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return hook
}
