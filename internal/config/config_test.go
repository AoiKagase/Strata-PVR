package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPreservesUnknownAndDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"recordedDir":"./rec/","custom":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RecordedDir != "./rec/" {
		t.Fatalf("RecordedDir = %q", cfg.RecordedDir)
	}
	if cfg.EffectiveMirakurunPath() != "http+unix://%2Fvar%2Frun%2Fmirakurun.sock/" {
		t.Fatalf("unexpected mirakurun path: %s", cfg.EffectiveMirakurunPath())
	}
	if _, ok := cfg.Raw["custom"]; !ok {
		t.Fatal("unknown field was not preserved in Raw")
	}
}
