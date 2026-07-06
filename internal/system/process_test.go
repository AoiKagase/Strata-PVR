package system

import (
	"os"
	"testing"
)

func TestProcessAlive(t *testing.T) {
	if !ProcessAlive(os.Getpid()) {
		t.Fatal("current process was not reported alive")
	}
	if ProcessAlive(-1) {
		t.Fatal("negative pid was reported alive")
	}
}
