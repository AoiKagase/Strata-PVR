package programstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"strata-pvr/internal/database"
	legacy "strata-pvr/internal/domain"
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
	if err := Write(ctx, databasePath, Recorded, []legacy.Program{{ID: "database", Recorded: "video.m2ts"}}); err != nil {
		t.Fatal(err)
	}
	got, err := Read(ctx, databasePath, Recorded)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "database" {
		t.Fatalf("programs = %#v", got)
	}
}

func TestCompleteMergesLatestRecordingState(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "strata.db")
	current := legacy.Program{
		ID:    "active",
		Title: "latest",
		Abort: true,
		PID:   -1,
		Raw: map[string]json.RawMessage{
			"external": json.RawMessage(`{"value":"keep"}`),
			"priority": json.RawMessage(`7`),
		},
	}
	if err := Upsert(ctx, databasePath, Recording, current); err != nil {
		t.Fatal(err)
	}

	completed := legacy.Program{
		ID:       current.ID,
		Title:    "stale",
		Recorded: "recorded/active.m2ts",
		PID:      0,
		Raw: map[string]json.RawMessage{
			"priority": json.RawMessage(`2`),
			"tuner":    json.RawMessage(`{"name":"Mirakurun"}`),
			"command":  json.RawMessage(`"record"`),
		},
	}
	if err := Complete(ctx, databasePath, completed); err != nil {
		t.Fatal(err)
	}

	got, found, err := ReadByID(ctx, databasePath, Recorded, current.ID)
	if err != nil || !found {
		t.Fatalf("completed program found=%v err=%v", found, err)
	}
	if got.Title != current.Title || !got.Abort || got.Recorded != completed.Recorded || got.PID != completed.PID {
		t.Fatalf("completion did not merge latest state: %#v", got)
	}
	if string(got.Raw["external"]) != `{"value":"keep"}` || string(got.Raw["priority"]) != `2` || string(got.Raw["tuner"]) != `{"name":"Mirakurun"}` || string(got.Raw["command"]) != `"record"` {
		t.Fatalf("completion raw fields = %#v", got.Raw)
	}
}

func TestCompleteReturnsNotFoundWithoutPromotingWhenRecordingDisappeared(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "strata.db")
	existing := legacy.Program{ID: "existing", Title: "keep"}
	if err := Upsert(ctx, databasePath, Recorded, existing); err != nil {
		t.Fatal(err)
	}

	err := Complete(ctx, databasePath, legacy.Program{ID: "active", Recorded: "recorded/active.m2ts"})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("completion error = %v, want sql.ErrNoRows", err)
	}
	if got, err := Read(ctx, databasePath, Recorded); err != nil || len(got) != 1 || got[0].ID != existing.ID {
		t.Fatalf("recorded collection after missing completion = %#v, err=%v", got, err)
	}
}

func TestClaimCompletionPreventsAbortUntilCompletion(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "strata.db")
	active := legacy.Program{ID: "active", Recorded: "recorded/active.m2ts"}
	if err := Upsert(ctx, databasePath, Recording, active); err != nil {
		t.Fatal(err)
	}
	claimed, err := ClaimCompletion(ctx, databasePath, active)
	if err != nil || !claimed {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	if err := SetAbort(ctx, databasePath, Recording, active.ID, true); !errors.Is(err, ErrProgramFinalizing) {
		t.Fatalf("abort while finalizing = %v, want ErrProgramFinalizing", err)
	}
	if err := Complete(ctx, databasePath, active); err != nil {
		t.Fatal(err)
	}
	completed, found, err := ReadByID(ctx, databasePath, Recorded, active.ID)
	if err != nil || !found {
		t.Fatalf("completed found=%v err=%v", found, err)
	}
	if _, ok := completed.Raw[completionMarker]; ok {
		t.Fatalf("completion marker leaked into recorded document: %#v", completed.Raw)
	}
}
