package domain

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

func TestSampleRulesLoad(t *testing.T) {
	const sampleRules = `[
  {"types":["GR"],"categories":["anime"],"ignore_channels":["26","27"],"hour":{"start":23,"end":4},"duration":{"min":600,"max":10801},"ignore_titles":["非公認戦隊アキバレンジャー","戦国コレクション","ＡＫＢ００４８"],"ignore_flags":["再"]},
  {"reserve_titles":["笑点"]},
  {"types":["CS"],"channels":["CS16"],"categories":["anime"],"sid":333,"duration":{"min":600,"max":10801}}
]`
	var rules []Rule
	if err := json.Unmarshal([]byte(sampleRules), &rules); err != nil {
		t.Fatal(err)
	}
	if len(rules) != 3 {
		t.Fatalf("sample rule count = %d", len(rules))
	}
	if len(rules[0].Types) == 0 || rules[0].Hour == nil || rules[0].Duration == nil {
		t.Fatalf("first sample rule did not load core fields: %#v", rules[0])
	}
	if len(rules[1].ReserveTitles) == 0 {
		t.Fatalf("second sample rule did not load reserve_titles: %#v", rules[1])
	}
	if rules[2].SID != 333 {
		t.Fatalf("third sample rule SID = %d", rules[2].SID)
	}
}

func TestProgramMatchesRuleUsesFullTitleLikeLegacyCommon(t *testing.T) {
	program := Program{
		Title:     "短い題名",
		FullTitle: "長い題名 特別版",
		Channel:   Channel{Type: "GR"},
	}
	if !ProgramMatchesRule(Rule{ReserveTitles: []string{"特別版"}}, program) {
		t.Fatal("reserve_titles should match fullTitle")
	}
	if ProgramMatchesRule(Rule{ReserveTitles: []string{"短い"}}, program) {
		t.Fatal("reserve_titles should not match title when fullTitle is present")
	}
	if ProgramMatchesRule(Rule{IgnoreTitles: []string{"特別版"}}, program) {
		t.Fatal("ignore_titles should reject fullTitle")
	}
	if !ProgramMatchesRuleForCLI(Rule{ReserveTitles: []string{"短い"}}, program) {
		t.Fatal("CLI reserve_titles should match title")
	}
	if ProgramMatchesRuleForCLI(Rule{ReserveTitles: []string{"特別版"}}, program) {
		t.Fatal("CLI reserve_titles should not match fullTitle")
	}
}

func TestProgramMatchesRuleCLIRequiresDetailForLegacyDescriptionAndReserveFlags(t *testing.T) {
	program := Program{Title: "番組", Flags: []string{"新"}, Channel: Channel{Type: "GR"}}
	if !ProgramMatchesRule(Rule{IgnoreDescriptions: []string{"再放送"}}, program) {
		t.Fatal("legacy common ignore_descriptions should allow a program without detail")
	}
	if !ProgramMatchesRule(Rule{ReserveFlags: []string{"新"}}, program) {
		t.Fatal("legacy common reserve_flags should match flags without requiring detail")
	}
	if ProgramMatchesRuleForCLI(Rule{IgnoreDescriptions: []string{"再放送"}}, program) {
		t.Fatal("CLI ignore_descriptions should not match a program without detail")
	}
	if ProgramMatchesRuleForCLI(Rule{ReserveFlags: []string{"新"}}, program) {
		t.Fatal("CLI reserve_flags should not match a program without detail like legacy app-cli")
	}
	program.Detail = "通常放送"
	if !ProgramMatchesRuleForCLI(Rule{IgnoreDescriptions: []string{"再放送"}}, program) {
		t.Fatal("CLI ignore_descriptions without a matching pattern should allow a detailed program")
	}
	if !ProgramMatchesRuleForCLI(Rule{ReserveFlags: []string{"新"}}, program) {
		t.Fatal("CLI reserve_flags should match when detail exists and flags overlap")
	}
}

func TestProgramMatchesRuleWithNormalizationForm(t *testing.T) {
	program := Program{FullTitle: "ＡＢＣ特集", Title: "ＡＢＣ", Detail: "第１話", Channel: Channel{Type: "GR"}}
	rule := Rule{ReserveTitles: []string{"ABC特集"}, ReserveDescriptions: []string{"第1話"}}
	if ProgramMatchesRule(rule, program) {
		t.Fatal("full-width text should not match ASCII rule without normalization")
	}
	if !ProgramMatchesRuleWithNormalization(rule, program, "NFKC") {
		t.Fatal("NFKC should match normalized title and detail")
	}
	if ProgramMatchesRuleForCLI(Rule{ReserveTitles: []string{"ABC"}}, program) {
		t.Fatal("CLI title should not match without normalization")
	}
	if !ProgramMatchesRuleForCLIWithNormalization(Rule{ReserveTitles: []string{"ABC"}}, program, "NFKC") {
		t.Fatal("CLI title should match with NFKC")
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
		t.Fatal("expected incomplete duration rule to be ignored like legacy runtime")
	}

	if err := json.Unmarshal([]byte(`{"duration":{"min":99999,"max":100000}}`), &rule); err != nil {
		t.Fatal(err)
	}
	if ProgramMatchesRule(rule, program) {
		t.Fatal("expected complete duration rule to reject")
	}
}

func TestProgramMatchesRuleChannelForms(t *testing.T) {
	program := Program{Channel: Channel{Type: "CS", Channel: "CS16", ID: "x1", SID: 333}, Title: "笑点", FullTitle: "笑点", Flags: []string{}}
	for _, channel := range []string{"x1", "CS16", "CS_333"} {
		if !ProgramMatchesRule(Rule{Channels: []string{channel}, ReserveTitles: []string{"笑点"}}, program) {
			t.Fatalf("expected channel %s to match", channel)
		}
	}
	if !ProgramMatchesRuleForCLI(Rule{Channels: []string{"CS16"}, ReserveTitles: []string{"笑点"}}, program) {
		t.Fatal("CLI channel should match program.channel.channel")
	}
	for _, channel := range []string{"x1", "CS_333"} {
		if ProgramMatchesRuleForCLI(Rule{Channels: []string{channel}, ReserveTitles: []string{"笑点"}}, program) {
			t.Fatalf("CLI channel %s should not match legacy app-cli.js filtering", channel)
		}
	}
	if ProgramMatchesRuleForCLI(Rule{IgnoreChannels: []string{"CS16"}, ReserveTitles: []string{"笑点"}}, program) {
		t.Fatal("CLI ignore_channels should reject program.channel.channel")
	}
	if !ProgramMatchesRuleForCLI(Rule{IgnoreChannels: []string{"x1"}, ReserveTitles: []string{"笑点"}}, program) {
		t.Fatal("CLI ignore_channels should not reject channel id")
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

func TestChannelJSONPreservesUnknownFields(t *testing.T) {
	var program Program
	if err := json.Unmarshal([]byte(`{"id":"abc","start":1,"end":2,"channel":{"id":"old","hasLogoData":true,"remoteControlKeyId":7}}`), &program); err != nil {
		t.Fatal(err)
	}
	if _, ok := program.Channel.Raw["remoteControlKeyId"]; !ok {
		t.Fatalf("channel unknown field was not preserved: %#v", program.Channel.Raw)
	}
	program.Channel.ID = "new"
	program.Channel.HasLogoData = false
	encoded, err := json.Marshal(program)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	var channel map[string]json.RawMessage
	if err := json.Unmarshal(raw["channel"], &channel); err != nil {
		t.Fatal(err)
	}
	if string(channel["remoteControlKeyId"]) != `7` {
		t.Fatalf("channel unknown field changed: %s", channel["remoteControlKeyId"])
	}
	if string(channel["id"]) != `"new"` {
		t.Fatalf("channel known field was not updated: %s", channel["id"])
	}
	if _, ok := channel["hasLogoData"]; ok {
		t.Fatalf("channel known zero field leaked from raw: %s", encoded)
	}
}

func TestChannelScheduleJSONKeepsFlattenedChannelAndPrograms(t *testing.T) {
	var schedule ChannelSchedule
	if err := json.Unmarshal([]byte(`{"id":"gr101","name":"GR 101","networkType":"GR","programs":[{"id":"p1","start":1,"end":2,"channel":{"id":"gr101"}}]}`), &schedule); err != nil {
		t.Fatal(err)
	}
	if schedule.ID != "gr101" || len(schedule.Programs) != 1 {
		t.Fatalf("unexpected schedule: %#v", schedule)
	}
	encoded, err := json.Marshal(schedule)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["networkType"]) != `"GR"` {
		t.Fatalf("channel schedule unknown field changed: %s", raw["networkType"])
	}
	if _, ok := raw["programs"]; !ok {
		t.Fatalf("programs missing from channel schedule: %s", encoded)
	}
}
