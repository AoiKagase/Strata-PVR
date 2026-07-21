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
	if marker := os.Getenv("STRATA_POST_PROCESS_MARKER"); marker != "" {
		f, err := os.OpenFile(marker, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = f.WriteString(strings.Join(os.Args, " ") + "\n")
			_ = f.Close()
		}
	}
	if os.Getenv("STRATA_POST_PROCESS_FAIL") == "1" || (os.Getenv("STRATA_POST_PROCESS_FAIL_ARGUMENT") != "" && strings.Contains(strings.Join(os.Args, "\x00"), os.Getenv("STRATA_POST_PROCESS_FAIL_ARGUMENT"))) {
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
	cfg := &config.Config{PostProcessCommands: []config.PostProcessCommand{{Command: os.Args[0], Arguments: []string{"-test.run=TestPostProcessHelper", "--"}}}, PostProcessTimeout: time.Second}
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
	cfg := &config.Config{PostProcessCommands: []config.PostProcessCommand{{Command: os.Args[0], Arguments: []string{"-test.run=TestPostProcessHelper", "--", "{recordedPath}", "{title}"}}}, PostProcessTimeout: time.Second}
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

func TestRunPostProcessRunsCommandsInOrderAndStopsAfterFailure(t *testing.T) {
	t.Setenv("STRATA_POST_PROCESS_HELPER", "1")
	t.Setenv("STRATA_POST_PROCESS_FAIL_ARGUMENT", "second")
	marker := t.TempDir() + "/commands.log"
	t.Setenv("STRATA_POST_PROCESS_MARKER", marker)
	logPath := t.TempDir() + "/operator.log"
	helper := func(argument string) config.PostProcessCommand {
		return config.PostProcessCommand{Command: os.Args[0], Arguments: []string{"-test.run=TestPostProcessHelper", "--", argument}}
	}
	cfg := &config.Config{PostProcessCommands: []config.PostProcessCommand{helper("first"), helper("second"), helper("third")}, PostProcessTimeout: time.Second}
	runPostProcess(logPath, cfg, legacy.Program{ID: "chain"})
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "first") || !strings.Contains(got, "second") || strings.Contains(got, "third") {
		t.Fatalf("command order = %q", got)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "remaining commands skipped") {
		t.Fatalf("post-process log = %s", log)
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
