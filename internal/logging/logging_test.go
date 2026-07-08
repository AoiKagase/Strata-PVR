package logging

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestAppendLinePrefixesLocalTimestamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log", "wui")
	if err := AppendLine(path, "SPAWN: %s", "ffmpeg"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} SPAWN: ffmpeg\n$`)
	if !pattern.Match(data) {
		t.Fatalf("unexpected log line: %q", string(data))
	}
}
