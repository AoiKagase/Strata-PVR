package scheduler

import (
	"testing"

	"chinachu-go/internal/config"
	"chinachu-go/internal/mirakurun"
)

func TestBuildScheduleFiltersAndOrders(t *testing.T) {
	cfg := &config.Config{ExcludeServices: []int64{2}, ServiceOrder: []int64{3}}
	services := []mirakurun.Service{
		{ID: 1, ServiceID: 101, NetworkID: 1, Name: "one"},
		{ID: 2, ServiceID: 102, NetworkID: 1, Name: "two"},
		{ID: 3, ServiceID: 103, NetworkID: 1, Name: "three"},
	}
	services[0].Channel.Type, services[0].Channel.Channel = "GR", "26"
	services[1].Channel.Type, services[1].Channel.Channel = "GR", "27"
	services[2].Channel.Type, services[2].Channel.Channel = "GR", "28"
	programs := []mirakurun.Program{
		{ID: 10, NetworkID: 1, ServiceID: 103, Name: "[新]Title", Description: "desc", StartAt: 1000, Duration: 60000, Genres: []mirakurun.Genre{{Lv1: 0x7}}},
	}
	schedule := BuildSchedule(cfg, services, programs)
	if len(schedule) != 2 {
		t.Fatalf("len(schedule) = %d", len(schedule))
	}
	if schedule[0].Name != "three" {
		t.Fatalf("first service = %s", schedule[0].Name)
	}
	if len(schedule[0].Programs) != 1 || schedule[0].Programs[0].Category != "anime" {
		t.Fatalf("unexpected programs: %#v", schedule[0].Programs)
	}
}
