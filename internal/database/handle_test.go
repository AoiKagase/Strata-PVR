package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAcquireReusesContextHandleWithoutClosingIt(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	shared, release, err := Acquire(WithHandle(ctx, db), filepath.Join(t.TempDir(), "unused.db"))
	if err != nil {
		t.Fatal(err)
	}
	if shared != db {
		t.Fatal("Acquire did not return the context database handle")
	}
	release()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("release closed the process-owned handle: %v", err)
	}
}

func TestAcquireStandaloneHandleIsReleased(t *testing.T) {
	ctx := context.Background()
	db, release, err := Acquire(ctx, filepath.Join(t.TempDir(), "strata.db"))
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := db.PingContext(ctx); err == nil {
		t.Fatal("standalone database handle remains open")
	}
}
