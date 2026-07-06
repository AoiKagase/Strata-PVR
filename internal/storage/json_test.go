package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAndReadJSONAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "state.json")
	in := []string{"a", "b"}
	if err := WriteJSONAtomic(path, in, false); err != nil {
		t.Fatal(err)
	}
	var out []string
	if err := ReadJSON(path, &out, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[1] != "b" {
		t.Fatalf("unexpected read: %#v", out)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestBackupFileCopiesCurrentContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	backup, err := BackupFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(backup) != filepath.Dir(path) {
		t.Fatalf("backup directory = %q", backup)
	}
	got, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("backup content = %q", got)
	}
}
