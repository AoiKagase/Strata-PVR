package database

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestReplaceSchedulerStateRollsBackBothCollections(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	oldSchedule := []ScheduleChannelDocument{{ChannelKey: "old", Document: json.RawMessage(`{"id":"old"}`)}}
	oldReservations := []ReservationDocument{{ProgramID: "old", Document: json.RawMessage(`{"id":"old"}`)}}
	if err := ReplaceSchedulerState(ctx, db, oldSchedule, oldReservations); err != nil {
		t.Fatal(err)
	}
	newSchedule := []ScheduleChannelDocument{{ChannelKey: "new", Document: json.RawMessage(`{"id":"new"}`)}}
	duplicate := ReservationDocument{ProgramID: "duplicate", Document: json.RawMessage(`{"id":"duplicate"}`)}
	if err := ReplaceSchedulerState(ctx, db, newSchedule, []ReservationDocument{duplicate, duplicate}); err == nil {
		t.Fatal("ReplaceSchedulerState accepted duplicate reservations")
	}
	schedule, err := ReadSchedule(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	reservations, err := ReadReservations(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(schedule) != 1 || schedule[0].ChannelKey != "old" || len(reservations) != 1 || string(reservations[0]) != `{"id":"old"}` {
		t.Fatalf("scheduler state was partially replaced: schedule=%#v reservations=%s", schedule, reservations)
	}
}
