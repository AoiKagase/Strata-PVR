package database

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestProgramCollectionsRemainIndependent(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recording := ProgramDocument{ProgramID: "active", Document: json.RawMessage(`{"id":"active"}`)}
	recorded := ProgramDocument{ProgramID: "library", Document: json.RawMessage(`{"id":"library"}`)}
	if err := ReplaceProgramCollection(ctx, db, "recording", []ProgramDocument{recording}); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceProgramCollection(ctx, db, "recorded", []ProgramDocument{recorded}); err != nil {
		t.Fatal(err)
	}
	active, err := ReadProgramCollection(ctx, db, "recording")
	if err != nil {
		t.Fatal(err)
	}
	library, err := ReadProgramCollection(ctx, db, "recorded")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || string(active[0]) != string(recording.Document) || len(library) != 1 || string(library[0]) != string(recorded.Document) {
		t.Fatalf("recording=%s recorded=%s", active, library)
	}
}

func TestProgramCollectionRejectsUnknownCollection(t *testing.T) {
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := ReplaceProgramCollection(context.Background(), db, "unknown", nil); err == nil {
		t.Fatal("unknown collection was accepted")
	}
}

func TestCompleteProgramPreservesUnrelatedPrograms(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	active := ProgramDocument{ProgramID: "active", Document: json.RawMessage(`{"id":"active"}`)}
	otherActive := ProgramDocument{ProgramID: "other-active", Document: json.RawMessage(`{"id":"other-active"}`)}
	existing := ProgramDocument{ProgramID: "existing", Document: json.RawMessage(`{"id":"existing"}`)}
	if err := ReplaceProgramCollection(ctx, db, "recording", []ProgramDocument{active, otherActive}); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceProgramCollection(ctx, db, "recorded", []ProgramDocument{existing}); err != nil {
		t.Fatal(err)
	}
	completed := ProgramDocument{ProgramID: "active", Document: json.RawMessage(`{"id":"active","recorded":"video.m2ts"}`)}
	if err := CompleteProgram(ctx, db, completed); err != nil {
		t.Fatal(err)
	}
	recording, err := ReadProgramCollection(ctx, db, "recording")
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := ReadProgramCollection(ctx, db, "recorded")
	if err != nil {
		t.Fatal(err)
	}
	if len(recording) != 1 || string(recording[0]) != string(otherActive.Document) {
		t.Fatalf("recording=%s", recording)
	}
	if len(recorded) != 2 || string(recorded[0]) != string(existing.Document) || string(recorded[1]) != string(completed.Document) {
		t.Fatalf("recorded=%s", recorded)
	}
}
