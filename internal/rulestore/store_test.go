package rulestore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"strata-pvr/internal/database"
	"strata-pvr/internal/legacy"
	"strata-pvr/internal/storage"
)

func TestSQLiteStoreIsCanonicalWhenDatabasePathIsSet(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "strata.db")
	jsonPath := filepath.Join(dir, "rules.json")
	if err := storage.WriteJSONAtomic(jsonPath, []legacy.Rule{{ReserveTitles: []string{"stale"}}}, true); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	want := []legacy.Rule{{ReserveTitles: []string{"database"}}}
	if err := Write(ctx, databasePath, jsonPath, want); err != nil {
		t.Fatal(err)
	}
	got, err := Read(ctx, databasePath, jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].ReserveTitles) != 1 || got[0].ReserveTitles[0] != "database" {
		t.Fatalf("rules = %#v", got)
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || !bytes.Contains(data, []byte("stale")) {
		t.Fatalf("legacy JSON projection was unexpectedly modified: %s", data)
	}
}

func TestJSONStoreRemainsAvailableWithoutDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "rules.json")
	want := []legacy.Rule{{ReserveTitles: []string{"legacy"}}}
	if err := Write(ctx, "", path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Read(ctx, "", path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ReserveTitles[0] != "legacy" {
		t.Fatalf("rules = %#v", got)
	}
}
