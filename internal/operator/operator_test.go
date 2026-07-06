package operator

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chinachu-go/internal/chinachu"
	"chinachu-go/internal/config"
	"chinachu-go/internal/storage"
)

type fakeStreamer struct {
	id     int64
	decode bool
	body   string
}

func (f *fakeStreamer) ProgramStream(_ context.Context, id int64, decode bool) (io.ReadCloser, error) {
	f.id = id
	f.decode = decode
	return io.NopCloser(strings.NewReader(f.body)), nil
}

func TestRunOnceRecordsDueProgram(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	paths := Paths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
	}
	program := chinachu.Program{
		ID:       "21i3v9",
		Title:    "Test/Program",
		Start:    now.Add(10 * time.Second).UnixMilli(),
		End:      now.Add(time.Hour).UnixMilli(),
		Category: "anime",
		Channel:  chinachu.Channel{Type: "GR", Channel: "27", Name: "Service"},
	}
	if err := storage.WriteJSONAtomic(paths.Reserves, []chinachu.Program{program}, false); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		RecordedDir:    filepath.Join(dir, "recorded"),
		RecordedFormat: "<id>-<title>.m2ts",
	}
	streamer := &fakeStreamer{body: "tsdata"}
	result, err := RunOnce(context.Background(), paths, cfg, streamer, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Started != 1 || result.Completed != 1 || result.Failed != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if streamer.id != 123456789 || !streamer.decode {
		t.Fatalf("unexpected stream request id=%d decode=%v", streamer.id, streamer.decode)
	}
	var reserves []chinachu.Program
	if err := storage.ReadJSON(paths.Reserves, &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 0 {
		t.Fatalf("reserves not cleared: %#v", reserves)
	}
	var recording []chinachu.Program
	if err := storage.ReadJSON(paths.Recording, &recording, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(recording) != 0 {
		t.Fatalf("recording not cleared: %#v", recording)
	}
	var recorded []chinachu.Program
	if err := storage.ReadJSON(paths.Recorded, &recorded, "[]"); err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || recorded[0].Recorded == "" {
		t.Fatalf("recorded entry missing: %#v", recorded)
	}
	data, err := os.ReadFile(filepath.FromSlash(recorded[0].Recorded))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "tsdata" {
		t.Fatalf("recorded data = %q", data)
	}
}

func TestRunOnceSkipsConflictAndFuturePrograms(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1000, 0)
	paths := Paths{
		Reserves:  filepath.Join(dir, "data", "reserves.json"),
		Recording: filepath.Join(dir, "data", "recording.json"),
		Recorded:  filepath.Join(dir, "data", "recorded.json"),
	}
	reserves := []chinachu.Program{
		{ID: "a", Start: now.Add(time.Minute).UnixMilli(), End: now.Add(time.Hour).UnixMilli()},
		{ID: "b", Start: now.UnixMilli(), End: now.Add(time.Hour).UnixMilli(), IsSkip: true},
		{ID: "c", Start: now.UnixMilli(), End: now.Add(time.Hour).UnixMilli(), IsConflict: true},
	}
	if err := storage.WriteJSONAtomic(paths.Reserves, reserves, false); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{RecordedDir: filepath.Join(dir, "recorded"), RecordedFormat: "<id>.m2ts"}
	result, err := RunOnce(context.Background(), paths, cfg, &fakeStreamer{body: "x"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Started != 0 || result.Completed != 0 || result.Failed != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
}
