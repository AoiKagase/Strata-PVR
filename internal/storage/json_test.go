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
