package database

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestScheduleRoundTripPreservesChannelAndProgramOrder(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	want := []ScheduleChannelDocument{
		{ChannelKey: "ch2", Document: json.RawMessage(`{"id":"ch2"}`), Programs: []ScheduleProgramDocument{{ProgramID: "p2", Start: 200, End: 300, Document: json.RawMessage(`{"id":"p2"}`)}}},
		{ChannelKey: "ch1", Document: json.RawMessage(`{"id":"ch1"}`), Programs: []ScheduleProgramDocument{{ProgramID: "p1", Start: 100, End: 150, Document: json.RawMessage(`{"id":"p1"}`)}}},
	}
	if err := ReplaceSchedule(ctx, db, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSchedule(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ChannelKey != "ch2" || got[1].ChannelKey != "ch1" || got[0].Programs[0].ProgramID != "p2" {
		t.Fatalf("schedule = %#v", got)
	}
}

func TestReplaceScheduleRollsBackDuplicateProgramID(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	kept := ScheduleChannelDocument{ChannelKey: "kept", Document: json.RawMessage(`{"id":"kept"}`)}
	if err := ReplaceSchedule(ctx, db, []ScheduleChannelDocument{kept}); err != nil {
		t.Fatal(err)
	}
	duplicate := ScheduleProgramDocument{ProgramID: "duplicate", Document: json.RawMessage(`{"id":"duplicate"}`)}
	invalid := ScheduleChannelDocument{ChannelKey: "new", Document: json.RawMessage(`{"id":"new"}`), Programs: []ScheduleProgramDocument{duplicate, duplicate}}
	if err := ReplaceSchedule(ctx, db, []ScheduleChannelDocument{invalid}); err == nil {
		t.Fatal("ReplaceSchedule accepted a duplicate program ID")
	}
	got, err := ReadSchedule(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ChannelKey != "kept" {
		t.Fatalf("rollback did not preserve schedule: %#v", got)
	}
}
