package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chinachu-go/internal/chinachu"
	"chinachu-go/internal/storage"
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

func TestUpdaterAcceptedWithoutNodeRuntime(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"updater"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "automatic git/service/installer operations are intentionally not performed") || !strings.Contains(text, "Node.js/npm modules are not required") {
		t.Fatalf("unexpected updater output: %s", text)
	}
}

func TestServiceInitscriptIncludesRestart(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"service", "operator", "initscript"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"DAEMON=./chinachu-go",
		`DAEMON_OPTS="service operator execute"`,
		"restart )",
		"sleep 3",
		"ps -p $PID",
		"Usage: $NAME {start|stop|restart|status}",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("initscript missing %q: %s", want, text)
		}
	}
}

func TestTestCommandAcceptedWithoutUsrBinExecution(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"test", "ffmpeg", "-version"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "usr/bin/ffmpeg is not executed") || !strings.Contains(text, "Node.js/npm modules are not required") {
		t.Fatalf("unexpected test command output: %s", text)
	}
}

func TestIRCBotAcceptedAsUnimplementedGoRuntimeFeature(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"ircbot"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "experimental Node-era IRC bot is not implemented") || !strings.Contains(text, "Go API") {
		t.Fatalf("unexpected ircbot output: %s", text)
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

func TestRuleLifecycle(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("rules.json", []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"rule", "-type", "GR", "-title", "笑点"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("rules.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"types": [`) || !strings.Contains(string(b), `"笑点"`) {
		t.Fatalf("rule not written: %s", string(b))
	}
	if err := Run(context.Background(), []string{"disrule", "0"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile("rules.json")
	if !strings.Contains(string(b), `"isDisabled": true`) {
		t.Fatalf("rule not disabled: %s", string(b))
	}
	if err := Run(context.Background(), []string{"rmrule", "0"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile("rules.json")
	if strings.TrimSpace(string(b)) != "[]" {
		t.Fatalf("rule not removed: %s", string(b))
	}
}

func TestRuleCommandDeletesNullMarkers(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	initial := `[{"types":["GR"],"reserve_titles":["Title"],"hour":{"start":1,"end":3},"duration":{"min":60,"max":3600}}]`
	if err := os.WriteFile("rules.json", []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"rule", "-n", "0", "-title", "null", "-start", "-1", "-mini", "-1"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("rules.json")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if strings.Contains(text, "reserve_titles") || strings.Contains(text, "hour") || strings.Contains(text, "duration") {
		t.Fatalf("rule deletion markers were not applied: %s", text)
	}
	if !strings.Contains(text, `"types": [`) || !strings.Contains(text, `"GR"`) {
		t.Fatalf("remaining rule condition was lost: %s", text)
	}
}

func TestSearchFiltersSchedule(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	schedule := `[{"type":"GR","channel":"27","name":"svc","id":"s","sid":101,"programs":[{"id":"p1","category":"anime","title":"Alpha","fullTitle":"Alpha","start":1893456000000,"end":1893457800000,"seconds":1800,"channel":{"type":"GR","channel":"27","name":"svc","id":"s","sid":101}},{"id":"p2","category":"news","title":"Beta","fullTitle":"Beta","start":1893459600000,"end":1893461400000,"seconds":1800,"channel":{"type":"GR","channel":"27","name":"svc","id":"s","sid":101}}]}]`
	if err := os.WriteFile(filepath.Join("data", "schedule.json"), []byte(schedule), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"search", "-title", "Alpha"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "p1") || strings.Contains(out.String(), "p2") {
		t.Fatalf("unexpected search output: %s", out.String())
	}
}

func TestStopMarksRecordingAbortAndAutoReserveSkip(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "recording.json"), []chinachu.Program{{ID: "auto"}, {ID: "manual", IsManualReserved: true}}, false); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSONAtomic(filepath.Join("data", "reserves.json"), []chinachu.Program{{ID: "auto"}, {ID: "manual", IsManualReserved: true}}, false); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"stop", "auto"}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var recording []chinachu.Program
	if err := storage.ReadJSON(filepath.Join("data", "recording.json"), &recording, "[]"); err != nil {
		t.Fatal(err)
	}
	if !recording[0].Abort {
		t.Fatalf("recording abort was not set: %#v", recording)
	}
	var reserves []chinachu.Program
	if err := storage.ReadJSON(filepath.Join("data", "reserves.json"), &reserves, "[]"); err != nil {
		t.Fatal(err)
	}
	if !reserves[0].IsSkip || reserves[1].IsSkip {
		t.Fatalf("auto reserve skip was not updated correctly: %#v", reserves)
	}
}
