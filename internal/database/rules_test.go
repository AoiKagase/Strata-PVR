package database

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestRulesRoundTripPreservesOrder(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	want := []json.RawMessage{json.RawMessage(`{"name":"first"}`), json.RawMessage(`{"name":"second"}`)}
	if err := ReplaceRules(ctx, db, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRules(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || string(got[0]) != string(want[0]) || string(got[1]) != string(want[1]) {
		t.Fatalf("rules = %s", got)
	}
}

func TestReplaceRulesRollsBackInvalidInput(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := ReplaceRules(ctx, db, []json.RawMessage{json.RawMessage(`{"name":"kept"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceRules(ctx, db, []json.RawMessage{json.RawMessage(`[]`)}); err == nil {
		t.Fatal("ReplaceRules accepted a non-object rule")
	}
	got, err := ReadRules(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0]) != `{"name":"kept"}` {
		t.Fatalf("rollback did not preserve rules: %s", got)
	}
}
