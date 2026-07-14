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

func TestAppendLineRotatesBySizeAndRetainsConfiguredCopies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log", "events")
	oldConfig := currentRotationConfig()
	if err := SetRotationConfig(RotationConfig{MaxBytes: 1, MaxFiles: 2}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = SetRotationConfig(oldConfig) })

	for _, message := range []string{"first", "second", "third"} {
		if err := AppendLine(path, "%s", message); err != nil {
			t.Fatal(err)
		}
	}

	assertFileContains(t, path, "third")
	assertFileContains(t, path+".1", "second")
	assertFileContains(t, path+".2", "first")
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("unexpected third rotated copy: %v", err)
	}
}

func TestSetRotationConfigRejectsInvalidValues(t *testing.T) {
	if err := SetRotationConfig(RotationConfig{MaxBytes: -1, MaxFiles: 1}); err == nil {
		t.Fatal("negative max bytes should be rejected")
	}
	if err := SetRotationConfig(RotationConfig{MaxBytes: 1, MaxFiles: 0}); err == nil {
		t.Fatal("zero max files should be rejected when rotation is enabled")
	}
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("%s does not contain %q: %s", path, want, data)
	}
}
