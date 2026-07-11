package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendLineWritesStructuredCompatibilityRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log", "wui")
	oldNow := now
	now = func() time.Time { return time.Date(2026, 7, 11, 12, 34, 56, 123000000, time.FixedZone("JST", 9*60*60)) }
	t.Cleanup(func() { now = oldNow })
	if err := AppendLine(path, "SPAWN: %s", "ffmpeg"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := string(data)
	if !strings.HasPrefix(line, "2026-07-11T12:34:56.123+09:00|level=info|event=spawn|message=") || !strings.Contains(line, `SPAWN: ffmpeg`) || !strings.HasSuffix(line, "\n") {
		t.Fatalf("unexpected log line: %q", string(data))
	}
}

func TestAppendEventEscapesValuesWithoutCreatingExtraLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log", "events")
	if err := Warn(path, "http.request", Field{Key: "status", Value: 400}, Field{Key: "message", Value: "bad|request\nquoted"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(data), "\n"); lines != 1 {
		t.Fatalf("record split into %d physical lines: %q", lines, string(data))
	}
	if !strings.Contains(string(data), `|level=warn|event=http.request|status=400|message=bad\u007crequest\nquoted`) {
		t.Fatalf("unexpected escaped record: %q", string(data))
	}
}

func TestAppendLineEmptyPathDoesNotCreateFiles(t *testing.T) {
	if err := AppendLine("", "ignored"); err != nil {
		t.Fatal(err)
	}
}
