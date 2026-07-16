package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFormatRecordedName(t *testing.T) {
	program := Program{
		ID:       "abc",
		Title:    `A/B:C*D?E"F<G>H|I`,
		Start:    time.Date(2024, 7, 1, 23, 30, 0, 0, time.Local).UnixMilli(),
		Category: "anime",
		Channel:  Channel{Type: "GR", Channel: "27", Name: "Test/Channel", SID: 101},
	}
	got := FormatRecordedName(program, "[<date:yymmdd-HHMM>][<type><channel>][<channel-name>]<title>.m2ts")
	want := "[240701-2330][GR27][Test／Channel]A／B：C＊D？E”F＜G＞H｜I.m2ts"
	if got != want {
		t.Fatalf("FormatRecordedName() = %q, want %q", got, want)
	}
}

func TestFormatRecordedNameSanitizesMirakurunTokens(t *testing.T) {
	program := Program{
		ID: "../id", Category: "../category",
		Channel: Channel{Type: "../type", Channel: `\\channel`, ID: `C:\\channel-id`},
		Raw:     map[string]json.RawMessage{"tuner": json.RawMessage(`{"name":"../tuner"}`)},
	}
	got := FormatRecordedName(program, "<id>-<type>-<channel>-<channel-id>-<tuner>-<category>.m2ts")
	if strings.ContainsAny(got, `\\/`) {
		t.Fatalf("unsafe token output: %q", got)
	}
}

func TestFormatRecordedNameLegacyDateMasks(t *testing.T) {
	oldLocal := time.Local
	time.Local = time.FixedZone("JST", 9*60*60)
	defer func() { time.Local = oldLocal }()

	program := Program{
		ID:      "abc",
		Title:   "Title",
		Start:   time.Date(2024, 7, 1, 23, 5, 6, 0, time.Local).UnixMilli(),
		Channel: Channel{Type: "GR", Channel: "27", Name: "Test", SID: 101},
	}
	got := FormatRecordedName(program, "<date:isoDateTime>-<date:shortTime>-<date:dddd>-<title>.m2ts")
	want := "2024-07-01T23:05:06-11:05 PM-Monday-Title.m2ts"
	if got != want {
		t.Fatalf("FormatRecordedName() = %q, want %q", got, want)
	}
}

func TestFormatRecordedNameLegacyNamedDateMasks(t *testing.T) {
	oldLocal := time.Local
	time.Local = time.FixedZone("JST", 9*60*60)
	defer func() { time.Local = oldLocal }()

	program := Program{
		ID:      "abc",
		Title:   "Title",
		Start:   time.Date(2024, 7, 1, 23, 5, 6, 0, time.Local).UnixMilli(),
		Channel: Channel{Type: "GR", Channel: "27", Name: "Test", SID: 101},
	}
	tests := []struct {
		mask string
		want string
	}{
		{"default", "Mon Jul 01 2024 23:05:06"},
		{"shortDate", "7/1/24"},
		{"mediumDate", "Jul 1, 2024"},
		{"longDate", "July 1, 2024"},
		{"fullDate", "Monday, July 1, 2024"},
		{"shortTime", "11:05 PM"},
		{"mediumTime", "11:05:06 PM"},
		{"longTime", "11:05:06 PM JST"},
		{"isoDate", "2024-07-01"},
		{"isoTime", "23:05:06"},
		{"isoDateTime", "2024-07-01T23:05:06"},
		{"expiresHeader", "Mon, 01 Jul 2024 23:05:06 JST"},
	}
	for _, tt := range tests {
		got := FormatRecordedName(program, "<date:"+tt.mask+">.m2ts")
		want := tt.want + ".m2ts"
		if got != want {
			t.Fatalf("FormatRecordedName(%q) = %q, want %q", tt.mask, got, want)
		}
	}
}

func TestFormatRecordedNameLegacyUTCDateMask(t *testing.T) {
	oldLocal := time.Local
	time.Local = time.FixedZone("JST", 9*60*60)
	defer func() { time.Local = oldLocal }()

	program := Program{
		ID:      "abc",
		Title:   "Title",
		Start:   time.Date(2024, 7, 1, 23, 5, 6, 0, time.Local).UnixMilli(),
		Channel: Channel{Type: "GR", Channel: "27", Name: "Test", SID: 101},
	}
	got := FormatRecordedName(program, "<date:isoUtcDateTime>.m2ts")
	want := "2024-07-01T14:05:06Z.m2ts"
	if got != want {
		t.Fatalf("FormatRecordedName() = %q, want %q", got, want)
	}
}

func TestFormatRecordedNameLegacyUTCDatePrefix(t *testing.T) {
	oldLocal := time.Local
	time.Local = time.FixedZone("JST", 9*60*60)
	defer func() { time.Local = oldLocal }()

	program := Program{
		ID:      "abc",
		Title:   "Title",
		Start:   time.Date(2024, 7, 1, 23, 5, 6, 0, time.Local).UnixMilli(),
		Channel: Channel{Type: "GR", Channel: "27", Name: "Test", SID: 101},
	}
	got := FormatRecordedName(program, "<date:UTC:yymmdd-HHMM>.m2ts")
	want := "240701-1405.m2ts"
	if got != want {
		t.Fatalf("FormatRecordedName() = %q, want %q", got, want)
	}
}

func TestFormatRecordedNameAdditionalDateformatTokens(t *testing.T) {
	oldLocal := time.Local
	time.Local = time.FixedZone("JST", 9*60*60)
	defer func() { time.Local = oldLocal }()

	program := Program{
		ID:      "abc",
		Title:   "Title",
		Start:   time.Date(2024, 7, 1, 23, 5, 6, 0, time.Local).UnixMilli(),
		Channel: Channel{Type: "GR", Channel: "27", Name: "Test", SID: 101},
	}
	got := FormatRecordedName(program, "<date:dS>-<date:o>-<date:expiresHeader>.m2ts")
	want := "1st-+0900-Mon, 01 Jul 2024 23:05:06 JST.m2ts"
	if got != want {
		t.Fatalf("FormatRecordedName() = %q, want %q", got, want)
	}
}

func TestFormatRecordedNameLegacyMillisecondDateformatTokens(t *testing.T) {
	oldLocal := time.Local
	time.Local = time.FixedZone("JST", 9*60*60)
	defer func() { time.Local = oldLocal }()

	program := Program{
		ID:      "abc",
		Title:   "Title",
		Start:   time.Date(2024, 7, 1, 23, 5, 6, 789*int(time.Millisecond), time.Local).UnixMilli(),
		Channel: Channel{Type: "GR", Channel: "27", Name: "Test", SID: 101},
	}
	got := FormatRecordedName(program, "<date:HHMMss.l-L>.m2ts")
	want := "230506.789-79.m2ts"
	if got != want {
		t.Fatalf("FormatRecordedName() = %q, want %q", got, want)
	}
}

func TestFormatRecordedNameLegacyTunerAndEpisodeTokens(t *testing.T) {
	program := Program{
		ID:       "abc",
		Title:    "Title",
		Start:    time.Date(2024, 7, 1, 23, 30, 0, 0, time.Local).UnixMilli(),
		Category: "anime",
		Channel:  Channel{Type: "GR", Channel: "27", Name: "Test", SID: 101},
		Raw: map[string]json.RawMessage{
			"tuner":   json.RawMessage(`{"name":"Mirakurun"}`),
			"episode": json.RawMessage(`7`),
		},
	}
	got := FormatRecordedName(program, "<tuner>-<episode>-<episode:03>-<title>.m2ts")
	want := "Mirakurun-7-007-Title.m2ts"
	if got != want {
		t.Fatalf("FormatRecordedName() = %q, want %q", got, want)
	}
}

func TestFormatRecordedNameLegacyEpisodeZeroFallback(t *testing.T) {
	program := Program{
		ID:      "abc",
		Title:   "Title",
		Start:   time.Date(2024, 7, 1, 23, 30, 0, 0, time.Local).UnixMilli(),
		Channel: Channel{Type: "GR", Channel: "27", Name: "Test", SID: 101},
		Raw: map[string]json.RawMessage{
			"episode": json.RawMessage(`0`),
		},
	}
	got := FormatRecordedName(program, "<episode>-<episode:02>.m2ts")
	want := "n-00.m2ts"
	if got != want {
		t.Fatalf("FormatRecordedName() = %q, want %q", got, want)
	}
}

func TestFormatRecordedNameLegacyUndefinedTokens(t *testing.T) {
	program := Program{
		ID:      "abc",
		Title:   "Title",
		Start:   time.Date(2024, 7, 1, 23, 30, 0, 0, time.Local).UnixMilli(),
		Channel: Channel{Type: "GR", Channel: "27", Name: "Test", SID: 101},
		Raw: map[string]json.RawMessage{
			"episode": json.RawMessage(`7`),
		},
	}
	got := FormatRecordedName(program, "<unknown>-<date:>-<episode:x>.m2ts")
	want := "undefined-undefined-undefined.m2ts"
	if got != want {
		t.Fatalf("FormatRecordedName() = %q, want %q", got, want)
	}
}
