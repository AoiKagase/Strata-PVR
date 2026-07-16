package operatorcontrol

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNotifyWakesListenerAndCloseRemovesSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operator.sock")
	listener, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Notify(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	select {
	case <-listener.Wake():
	case <-time.After(time.Second):
		t.Fatal("operator was not woken")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket still exists after close: %v", err)
	}
}

func TestListenRejectsRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operator.sock")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(path); err == nil {
		t.Fatal("Listen accepted a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep" {
		t.Fatalf("regular file was changed: data=%q err=%v", data, err)
	}
}
