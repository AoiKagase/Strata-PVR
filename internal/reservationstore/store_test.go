package reservationstore

import (
	"context"
	"path/filepath"
	"testing"

	"strata-pvr/internal/database"
	"strata-pvr/internal/legacy"
	"strata-pvr/internal/storage"
)

func TestSQLiteReservationStoreIgnoresLegacyJSON(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "strata.db")
	jsonPath := filepath.Join(dir, "reserves.json")
	if err := storage.WriteJSONAtomic(jsonPath, []legacy.Program{{ID: "stale"}}, false); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	want := []legacy.Program{{ID: "database", Start: 100, End: 200, IsManualReserved: true}}
	if err := Write(ctx, databasePath, jsonPath, want); err != nil {
		t.Fatal(err)
	}
	got, err := Read(ctx, databasePath, jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "database" || !got[0].IsManualReserved {
		t.Fatalf("reservations = %#v", got)
	}
}
