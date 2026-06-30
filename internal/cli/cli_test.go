package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelp(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), nil, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "reserve <pgid>") {
		t.Fatalf("help missing reserve: %s", out.String())
	}
}

func TestReserve(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	schedule := `[{"type":"GR","channel":"27","name":"svc","id":"s","sid":101,"programs":[{"id":"p1","title":"T","fullTitle":"T","start":1,"end":2,"seconds":1,"channel":{"type":"GR","channel":"27","name":"svc","id":"s","sid":101}}]}]`
	if err := os.WriteFile(filepath.Join("data", "schedule.json"), []byte(schedule), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("data", "reserves.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"reserve", "p1"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join("data", "reserves.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"isManualReserved":true`) {
		t.Fatalf("reserve file not updated: %s", string(b))
	}
}
