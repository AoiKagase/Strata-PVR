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
