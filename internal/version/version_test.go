package version

import "testing"

func TestCurrentUsesBuildDefaults(t *testing.T) {
	oldNumber, oldCommit, oldDate := Number, Commit, Date
	t.Cleanup(func() { Number, Commit, Date = oldNumber, oldCommit, oldDate })
	Number = "0.1.0-dev"
	Commit = "abc123"
	Date = "2026-07-12T00:00:00Z"

	got := Current()
	if got.Number != "0.1.0-dev+abc123" || got.Commit != "abc123" || got.Date != Date {
		t.Fatalf("Current() = %#v", got)
	}
}
