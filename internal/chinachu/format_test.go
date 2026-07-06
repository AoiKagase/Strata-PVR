package chinachu

import (
	"encoding/json"
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
