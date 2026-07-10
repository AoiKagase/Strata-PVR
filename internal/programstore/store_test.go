package programstore

import (
	"context"
	"path/filepath"
	"testing"

	"strata-pvr/internal/database"
	"strata-pvr/internal/legacy"
	"strata-pvr/internal/storage"
)

func TestSQLiteProgramStoreIgnoresLegacyJSON(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "strata.db")
	jsonPath := filepath.Join(dir, "recorded.json")
	if err := storage.WriteJSONAtomic(jsonPath, []legacy.Program{{ID: "stale"}}, false); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := Write(ctx, databasePath, jsonPath, Recorded, []legacy.Program{{ID: "database", Recorded: "video.m2ts"}}); err != nil {
		t.Fatal(err)
	}
	got, err := Read(ctx, databasePath, jsonPath, Recorded)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "database" {
		t.Fatalf("programs = %#v", got)
	}
}
