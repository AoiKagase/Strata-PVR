package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProgramJSONUsesLegacyKnownFieldOrder(t *testing.T) {
	var program Program
	if err := json.Unmarshal([]byte(`{"x-unknown":true,"channel":{"remoteControlKeyId":7,"type":"GR","channel":"27","name":"Svc","id":"ch","sid":101},"end":2,"id":"abc","start":1,"isSkip":true}`), &program); err != nil {
		t.Fatal(err)
	}
	program.IsManualReserved = true
	encoded, err := json.Marshal(program)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, pair := range [][2]string{
		{`"id"`, `"start"`},
		{`"start"`, `"end"`},
		{`"end"`, `"channel"`},
		{`"channel"`, `"isManualReserved"`},
		{`"isManualReserved"`, `"isSkip"`},
		{`"isSkip"`, `"x-unknown"`},
	} {
		if strings.Index(text, pair[0]) == -1 || strings.Index(text, pair[0]) > strings.Index(text, pair[1]) {
			t.Fatalf("program JSON order %s before %s not preserved: %s", pair[0], pair[1], text)
		}
	}
	if !strings.Contains(text, `"remoteControlKeyId":7`) {
		t.Fatalf("channel unknown field missing: %s", text)
	}
}

func TestChannelScheduleJSONPutsProgramsAfterChannelFields(t *testing.T) {
	schedule := ChannelSchedule{
		Channel:  Channel{Type: "GR", Channel: "27", Name: "Svc", ID: "ch", SID: 101},
		Programs: []Program{{ID: "p", Start: 1, End: 2, Channel: Channel{ID: "ch"}}},
	}
	encoded, err := json.Marshal(schedule)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Index(text, `"sid"`) == -1 || strings.Index(text, `"sid"`) > strings.Index(text, `"programs"`) {
		t.Fatalf("schedule JSON should keep channel fields before programs: %s", text)
	}
}

func TestRuleJSONUsesLegacyKnownFieldOrder(t *testing.T) {
	rule := Rule{
		IsDisabled:    true,
		Types:         []string{"GR"},
		Categories:    []string{"anime"},
		ReserveTitles: []string{"Title"},
	}
	encoded, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, pair := range [][2]string{
		{`"types"`, `"categories"`},
		{`"categories"`, `"reserve_titles"`},
		{`"reserve_titles"`, `"isDisabled"`},
	} {
		if strings.Index(text, pair[0]) == -1 || strings.Index(text, pair[0]) > strings.Index(text, pair[1]) {
			t.Fatalf("rule JSON order %s before %s not preserved: %s", pair[0], pair[1], text)
		}
	}
}
