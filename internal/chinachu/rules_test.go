package chinachu

import (
	"encoding/json"
	"testing"
)

func TestProgramMatchesRuleOvernightHour(t *testing.T) {
	program := Program{
		ID:        "x",
		Category:  "anime",
		FullTitle: "Example",
		Start:     1719846000000,
		End:       1719853200000,
		Seconds:   7200,
		Channel:   Channel{Type: "GR", Channel: "26", ID: "abc", SID: 101},
		Flags:     []string{"新"},
	}
	rule := Rule{
		Types:      []string{"GR"},
		Categories: []string{"anime"},
		Hour:       &RangeRule{Start: 23, End: 4},
		Duration:   &DurationRule{Min: 600, Max: 10801, HasMin: true, HasMax: true},
	}
	if !ProgramMatchesRule(rule, program) {
		t.Fatal("expected overnight rule to match")
	}
	rule.IgnoreFlags = []string{"新"}
	if ProgramMatchesRule(rule, program) {
		t.Fatal("expected ignore flag to reject")
	}
}

func TestProgramMatchesRuleIgnoresIncompleteJSONDuration(t *testing.T) {
	var rule Rule
	if err := json.Unmarshal([]byte(`{"duration":{"min":99999}}`), &rule); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"duration":{"min":99999}}` {
		t.Fatalf("incomplete duration was not preserved: %s", encoded)
	}
	program := Program{Seconds: 60, FullTitle: "Example", Channel: Channel{Type: "GR"}}
	if !ProgramMatchesRule(rule, program) {
		t.Fatal("expected incomplete duration rule to be ignored like legacy Chinachu")
	}

	if err := json.Unmarshal([]byte(`{"duration":{"min":99999,"max":100000}}`), &rule); err != nil {
		t.Fatal(err)
	}
	if ProgramMatchesRule(rule, program) {
		t.Fatal("expected complete duration rule to reject")
	}
}

func TestProgramMatchesRuleChannelForms(t *testing.T) {
	program := Program{Channel: Channel{Type: "CS", Channel: "CS16", ID: "x1", SID: 333}, FullTitle: "笑点", Flags: []string{}}
	for _, channel := range []string{"x1", "CS16", "CS_333"} {
		if !ProgramMatchesRule(Rule{Channels: []string{channel}, ReserveTitles: []string{"笑点"}}, program) {
			t.Fatalf("expected channel %s to match", channel)
		}
	}
}

func TestProgramJSONPreservesUnknownFields(t *testing.T) {
	var program Program
	if err := json.Unmarshal([]byte(`{"id":"abc","start":1,"end":2,"channel":{},"isSkip":true,"x-unknown":{"nested":true}}`), &program); err != nil {
		t.Fatal(err)
	}
	if _, ok := program.Raw["x-unknown"]; !ok {
		t.Fatalf("unknown field was not preserved: %#v", program.Raw)
	}
	program.IsSkip = false
	encoded, err := json.Marshal(program)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["x-unknown"]) != `{"nested":true}` {
		t.Fatalf("unknown field changed: %s", raw["x-unknown"])
	}
	if _, ok := raw["isSkip"]; ok {
		t.Fatalf("known zero field leaked from raw: %s", encoded)
	}
}
