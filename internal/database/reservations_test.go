package database

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestReservationsRoundTripPreservesOrder(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	want := []ReservationDocument{
		{ProgramID: "later", Start: 200, End: 300, Document: json.RawMessage(`{"id":"later"}`)},
		{ProgramID: "earlier", Start: 100, End: 150, Document: json.RawMessage(`{"id":"earlier"}`)},
	}
	if err := ReplaceReservations(ctx, db, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadReservations(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || string(got[0]) != string(want[0].Document) || string(got[1]) != string(want[1].Document) {
		t.Fatalf("reservations = %s", got)
	}
}

func TestReplaceReservationsRollsBackDuplicateProgramID(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	kept := ReservationDocument{ProgramID: "kept", Document: json.RawMessage(`{"id":"kept"}`)}
	if err := ReplaceReservations(ctx, db, []ReservationDocument{kept}); err != nil {
		t.Fatal(err)
	}
	duplicate := ReservationDocument{ProgramID: "duplicate", Document: json.RawMessage(`{"id":"duplicate"}`)}
	if err := ReplaceReservations(ctx, db, []ReservationDocument{duplicate, duplicate}); err == nil {
		t.Fatal("ReplaceReservations accepted a duplicate program ID")
	}
	got, err := ReadReservations(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0]) != string(kept.Document) {
		t.Fatalf("rollback did not preserve reservations: %s", got)
	}
}
