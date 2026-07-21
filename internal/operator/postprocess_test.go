package operator

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"strata-pvr/internal/config"
	legacy "strata-pvr/internal/domain"
)

func TestPostProcessHelper(t *testing.T) {
	if os.Getenv("STRATA_POST_PROCESS_HELPER") != "1" {
		return
	}
	data, _ := io.ReadAll(os.Stdin)
	if os.Getenv("STRATA_POST_PROCESS_FAIL") == "1" {
		_, _ = os.Stderr.WriteString("intentional failure")
		os.Exit(7)
	}
	_, _ = os.Stdout.Write(data)
	os.Exit(0)
}

func TestRunPostProcessLogsExitCode(t *testing.T) {
	t.Setenv("STRATA_POST_PROCESS_HELPER", "1")
	t.Setenv("STRATA_POST_PROCESS_FAIL", "1")
	logPath := t.TempDir() + "/operator.log"
	cfg := &config.Config{PostProcessCommand: []string{os.Args[0], "-test.run=TestPostProcessHelper", "--"}, PostProcessTimeout: time.Second}
	runPostProcess(logPath, cfg, legacy.Program{ID: "failed"})
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if line := string(data); !strings.Contains(line, "exit_code=7") || !strings.Contains(line, "intentional failure") {
		t.Fatalf("failed post-process log = %s", line)
	}
}

func TestRunPostProcessPassesProgramJSONAndLogsResult(t *testing.T) {
	t.Setenv("STRATA_POST_PROCESS_HELPER", "1")
	logPath := t.TempDir() + "/operator.log"
	program := legacy.Program{ID: "abc", Title: "Example", Recorded: "/recorded/example.m2ts", Channel: legacy.Channel{Name: "NHK", Type: "GR", Channel: "27"}}
	cfg := &config.Config{PostProcessCommand: []string{os.Args[0], "-test.run=TestPostProcessHelper", "--", "{recordedPath}", "{title}"}, PostProcessTimeout: time.Second}
	runPostProcess(logPath, cfg, program)
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	line := string(data)
	if !strings.Contains(line, "POSTPROCESS") || !strings.Contains(line, `\"recorded\":\"/recorded/example.m2ts\"`) {
		t.Fatalf("post-process log = %s", line)
	}
}

func TestPostProcessArgsDoesNotUseShellExpansion(t *testing.T) {
	program := legacy.Program{ID: "id;rm", Title: "$(unsafe)", Recorded: "/tmp/a file.m2ts"}
	got := postProcessArgs([]string{"hook", "{recordedPath}", "{programID}", "{title}"}, program)
	want := []string{"hook", "/tmp/a file.m2ts", "id;rm", "$(unsafe)"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("post-process argv = %#v, want %#v", got, want)
	}
}
