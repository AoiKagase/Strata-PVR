package scheduler

import (
	"testing"

	"strata-pvr/internal/config"
	"strata-pvr/internal/mirakurun"
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

func TestProgramFlagsKeepARIBExternalCharacters(t *testing.T) {
	title := "番組\ue0f8\ue0f9\ue0fa\ue0fb\ue0fc\ue0fd\ue0fe\ue0ff\ue180\ue181\ue182\ue183\ue184\ue185\ue186\ue187\ue18a\ue18b\ue18c\ue18d\ue18e⚿\ue190\ue191\ue192\ue193\ue194\ue195\ue196\ue197\ue198\ue199\ue19a㊙\ue19c"
	if got := stripProgramFlags(title); got != "番組" {
		t.Fatalf("stripProgramFlags = %q", got)
	}
	got := extractFlags(title)
	want := []string{"HV", "SD", "P", "W", "MV", "手", "字", "双", "デ", "S", "二", "多", "解", "SS", "B", "N", "天", "交", "映", "無", "料", "鍵マーク", "前", "後", "再", "新", "初", "終", "生", "販", "声", "吹", "PPV", "秘", "ほか"}
	if len(got) != len(want) {
		t.Fatalf("extractFlags = %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("extractFlags[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestProgramFlagsIncludeNewAndEnd(t *testing.T) {
	title := "[新][終]番組"
	if got := stripProgramFlags(title); got != "番組" {
		t.Fatalf("stripProgramFlags = %q", got)
	}
	got := extractFlags(title)
	want := []string{"新", "終"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("extractFlags = %#v, want %#v", got, want)
	}
}
