package system

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireProcessLockRejectsSecondHolderAndAllowsReacquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduler.pid.lock")
	first, err := AcquireProcessLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	if _, err := AcquireProcessLock(path); !errors.Is(err, ErrProcessAlreadyRunning) {
		t.Fatalf("second acquire error = %v, want ErrProcessAlreadyRunning", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := AcquireProcessLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireProcessLockReusesStaleLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operator.pid.lock")
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireProcessLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireProcessLockEmptyPathIsNoOp(t *testing.T) {
	lock, err := AcquireProcessLock("")
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}
