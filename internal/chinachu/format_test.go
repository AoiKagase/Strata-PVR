package chinachu

import "testing"

func TestFormatRecordedName(t *testing.T) {
	program := Program{
		ID:       "abc",
		Title:    `A/B:C*D?E"F<G>H|I`,
		Start:    1719846000000,
		Category: "anime",
		Channel:  Channel{Type: "GR", Channel: "27", Name: "Test/Channel", SID: 101},
	}
	got := FormatRecordedName(program, "[<date:yymmdd-HHMM>][<type><channel>][<channel-name>]<title>.m2ts")
	want := "[240701-2330][GR27][Test／Channel]A／B：C＊D？E”F＜G＞H｜I.m2ts"
	if got != want {
		t.Fatalf("FormatRecordedName() = %q, want %q", got, want)
	}
}
