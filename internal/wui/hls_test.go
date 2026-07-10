package wui

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestHLSFFmpegArgsUseIPhoneCompatibleCodecs(t *testing.T) {
	dir := t.TempDir()
	args := hlsFFmpegArgs("recording.m2ts", dir, hlsPresets["540p"], 12, 30, "secondary")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-ss 12", "-t 30", "-map 0:a:1?", "-c:v libopenh264", "-profile:v constrained_baseline", "-pix_fmt yuv420p",
		"-c:a aac", "-ac 2", "-ar 48000", "-hls_time 4", "-hls_playlist_type vod",
		filepath.Join(dir, "segment%05d.ts"),
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args do not contain %q: %s", want, joined)
		}
	}
}

func TestAddHLSSessionQueryOnlyChangesSegments(t *testing.T) {
	got := addHLSSessionQuery("#EXTM3U\n#EXTINF:4,\nsegment00001.ts\n#EXT-X-ENDLIST\n", "0123456789abcdef01234567")
	want := "segment00001.ts?session=0123456789abcdef01234567"
	if !strings.Contains(got, want) {
		t.Fatalf("playlist = %q, want it to contain %q", got, want)
	}
	if strings.Contains(got, "#EXTM3U?") {
		t.Fatalf("playlist metadata was modified: %q", got)
	}
}

func TestValidHLSSegmentName(t *testing.T) {
	for _, name := range []string{"segment00000.ts", "segment42.ts"} {
		if !validHLSSegmentName(name) {
			t.Errorf("expected valid: %q", name)
		}
	}
	for _, name := range []string{"../segment1.ts", "segment.ts", "segment1.mp4", "other1.ts"} {
		if validHLSSegmentName(name) {
			t.Errorf("expected invalid: %q", name)
		}
	}
}
