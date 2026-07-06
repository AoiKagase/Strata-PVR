package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"chinachu-go/internal/chinachu"
	"chinachu-go/internal/config"
	"chinachu-go/internal/mirakurun"
)

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
