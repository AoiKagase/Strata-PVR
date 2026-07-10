package schedulestore

import (
	"context"
	"path/filepath"
	"testing"

	"strata-pvr/internal/database"
	legacy "strata-pvr/internal/domain"
	"strata-pvr/internal/storage"
)

func TestSQLiteScheduleStoreIgnoresLegacyJSON(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "strata.db")
	jsonPath := filepath.Join(dir, "schedule.json")
	stale := []legacy.ChannelSchedule{{Channel: legacy.Channel{ID: "stale"}}}
	if err := storage.WriteJSONAtomic(jsonPath, stale, false); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	want := []legacy.ChannelSchedule{{
		Channel:  legacy.Channel{ID: "database", Name: "Database Channel"},
		Programs: []legacy.Program{{ID: "program-1", Start: 100, End: 200, Title: "Program"}},
	}}
	if err := Write(ctx, databasePath, want); err != nil {
		t.Fatal(err)
	}
	got, err := Read(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "database" || len(got[0].Programs) != 1 || got[0].Programs[0].ID != "program-1" {
		t.Fatalf("schedule = %#v", got)
	}
}
