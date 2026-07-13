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

func TestReadReservationsDueFiltersByStartAndEnd(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	reservations := []ReservationDocument{
		{ProgramID: "ended-at-boundary", Start: 900, End: 1000, Document: json.RawMessage(`{"id":"ended-at-boundary"}`)},
		{ProgramID: "due", Start: 900, End: 1100, Document: json.RawMessage(`{"id":"due"}`)},
		{ProgramID: "starts-at-boundary", Start: 1010, End: 1200, Document: json.RawMessage(`{"id":"starts-at-boundary"}`)},
		{ProgramID: "future", Start: 1200, End: 1300, Document: json.RawMessage(`{"id":"future"}`)},
	}
	if err := ReplaceReservations(ctx, db, reservations); err != nil {
		t.Fatal(err)
	}
	documents, err := ReadReservationsDue(ctx, db, 1010, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 2 || string(documents[0]) != `{"id":"due"}` || string(documents[1]) != `{"id":"starts-at-boundary"}` {
		t.Fatalf("due documents = %s", documents)
	}
}

func TestReadReservationsByIDsIncludesEndedReservationsInPositionOrder(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reservations := []ReservationDocument{
		{ProgramID: "ended", Start: 100, End: 200, Document: json.RawMessage(`{"id":"ended"}`)},
		{ProgramID: "active", Start: 300, End: 400, Document: json.RawMessage(`{"id":"active"}`)},
	}
	if err := ReplaceReservations(ctx, db, reservations); err != nil {
		t.Fatal(err)
	}
	documents, err := ReadReservationsByIDs(ctx, db, []string{"active", "ended"})
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 2 || string(documents[0]) != `{"id":"ended"}` || string(documents[1]) != `{"id":"active"}` {
		t.Fatalf("selected reservations = %s", documents)
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
