package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"strata-pvr/internal/config"
	legacy "strata-pvr/internal/domain"
	"strata-pvr/internal/logging"
)

const defaultPostProcessTimeout = 10 * time.Minute

var postProcessLimiters sync.Map // map[*config.Config]chan struct{}

// runPostProcess invokes the configured argv after a successful recording.
// Failures are deliberately logged and never returned: the recording is
// already complete and its file must remain available to the user.
func runPostProcess(logPath string, cfg *config.Config, program legacy.Program) {
	if cfg == nil || len(cfg.PostProcessCommand) == 0 {
		return
	}
	limiter := postProcessLimiter(cfg)
	limiter <- struct{}{}
	defer func() { <-limiter }()

	timeout := cfg.PostProcessTimeout
	if timeout == 0 {
		timeout = defaultPostProcessTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	args := postProcessArgs(cfg.PostProcessCommand, program)
	if len(args) == 0 || args[0] == "" {
		_ = logging.AppendLine(logPath, "ERROR: post-process %s: empty command", program.ID)
		return
	}
	payload, err := json.Marshal(program)
	if err != nil {
		_ = logging.AppendLine(logPath, "ERROR: post-process %s: encode program: %v", program.ID, err)
		return
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdin = bytes.NewReader(payload)
	output, err := cmd.CombinedOutput()
	result := compactPostProcessOutput(output)
	if ctx.Err() == context.DeadlineExceeded {
		_ = logging.AppendLine(logPath, "ERROR: post-process %s: timeout=%s output=%q", program.ID, timeout, result)
		return
	}
	if err != nil {
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		_ = logging.AppendLine(logPath, "ERROR: post-process %s: exit_code=%d error=%v output=%q", program.ID, exitCode, err, result)
		return
	}
	_ = logging.AppendLine(logPath, "POSTPROCESS: %s command=%q output=%q", program.ID, args, result)
}

func postProcessLimiter(cfg *config.Config) chan struct{} {
	limit := cfg.PostProcessMaxConcurrent
	if limit <= 0 {
		limit = 1
	}
	actual, _ := postProcessLimiters.LoadOrStore(cfg, make(chan struct{}, limit))
	return actual.(chan struct{})
}

func postProcessArgs(command []string, program legacy.Program) []string {
	replacements := map[string]string{
		"{recordedPath}":     program.Recorded,
		"{programID}":        program.ID,
		"{title}":            program.Title,
		"{channelName}":      program.Channel.Name,
		"{channelType}":      program.Channel.Type,
		"{channelNumber}":    program.Channel.Channel,
		"{startAtUnixMilli}": strconv.FormatInt(program.Start, 10),
		"{endAtUnixMilli}":   strconv.FormatInt(program.End, 10),
	}
	args := make([]string, len(command))
	for i, value := range command {
		for token, replacement := range replacements {
			value = strings.ReplaceAll(value, token, replacement)
		}
		args[i] = value
	}
	return args
}

func compactPostProcessOutput(output []byte) string {
	const maxOutput = 4096
	value := strings.TrimSpace(string(output))
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > maxOutput {
		return fmt.Sprintf("%s…", value[:maxOutput])
	}
	return value
}
