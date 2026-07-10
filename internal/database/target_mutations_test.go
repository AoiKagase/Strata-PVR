package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestReservationTargetMutationsPreserveOrder(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, item := range []ReservationDocument{
		{ProgramID: "later", Start: 200, Document: json.RawMessage(`{"id":"later","start":200}`)},
		{ProgramID: "latest", Start: 300, Document: json.RawMessage(`{"id":"latest","start":300}`)},
	} {
		if err := UpsertReservation(ctx, db, item); err != nil {
			t.Fatal(err)
		}
	}
	if err := UpsertReservation(ctx, db, ReservationDocument{ProgramID: "earlier", Start: 100, Document: json.RawMessage(`{"id":"earlier","start":100}`)}); err != nil {
		t.Fatal(err)
	}
	assertDocumentIDs(t, mustReadReservations(t, ctx, db), []string{"earlier", "later", "latest"})
	deleted, err := DeleteReservation(ctx, db, "later")
	if err != nil || !deleted {
		t.Fatalf("DeleteReservation() = %v, %v", deleted, err)
	}
	assertDocumentIDs(t, mustReadReservations(t, ctx, db), []string{"earlier", "latest"})
}

func TestRuleTargetMutationsPreservePositions(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, rule := range []json.RawMessage{json.RawMessage(`{"id":"a"}`), json.RawMessage(`{"id":"b"}`), json.RawMessage(`{"id":"c"}`)} {
		if err := AppendRule(ctx, db, rule); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := UpdateRule(ctx, db, 1, json.RawMessage(`{"id":"updated"}`)); err != nil || !ok {
		t.Fatalf("UpdateRule() = %v, %v", ok, err)
	}
	if ok, err := DeleteRule(ctx, db, 0); err != nil || !ok {
		t.Fatalf("DeleteRule() = %v, %v", ok, err)
	}
	rules, err := ReadRules(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	assertDocumentIDs(t, rules, []string{"updated", "c"})
}

func mustReadReservations(t *testing.T, ctx context.Context, db *sql.DB) []json.RawMessage {
	t.Helper()
	rows, err := db.QueryContext(ctx, "SELECT document_json FROM reservations ORDER BY position")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var documents []json.RawMessage
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		documents = append(documents, json.RawMessage(value))
	}
	return documents
}

func assertDocumentIDs(t *testing.T, documents []json.RawMessage, want []string) {
	t.Helper()
	got := make([]string, len(documents))
	for i, document := range documents {
		var value struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(document, &value); err != nil {
			t.Fatal(err)
		}
		got[i] = value.ID
	}
	if len(got) != len(want) {
		t.Fatalf("IDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("IDs = %v, want %v", got, want)
		}
	}
}
