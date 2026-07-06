package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"chinachu-go/internal/chinachu"
	"chinachu-go/internal/config"
	"chinachu-go/internal/logging"
	"chinachu-go/internal/mirakurun"
	"chinachu-go/internal/storage"
)

type Source interface {
	Services(context.Context) ([]mirakurun.Service, error)
	Programs(context.Context) ([]mirakurun.Program, error)
	Tuners(context.Context) ([]mirakurun.Tuner, error)
}

type Paths struct {
	Config   string
	Rules    string
	Schedule string
	Reserves string
	PID      string
	Log      string
}

type Result struct {
	Matches    int
	Duplicates int
	Conflicts  int
	Skips      int
	Reserves   int
}

func Run(ctx context.Context, paths Paths, simulation bool) (Result, error) {
	if err := writePIDFile(paths.PID); err != nil {
		return Result{}, err
	}
	defer removePIDFile(paths.PID)

	cfg, err := config.Load(paths.Config)
	if err != nil {
		return Result{}, err
	}
	client, err := mirakurun.New(cfg.EffectiveMirakurunPath())
	if err != nil {
		return Result{}, err
	}
	return RunWithSource(ctx, paths, cfg, client, simulation, time.Now())
}

func writePIDFile(path string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
}

func removePIDFile(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}

func RunWithSource(ctx context.Context, paths Paths, cfg *config.Config, source Source, simulation bool, now time.Time) (Result, error) {
	if err := logging.AppendLine(paths.Log, "RUNNING SCHEDULER."); err != nil {
		return Result{}, err
	}
	if !simulation {
		if err := runHook(ctx, paths.Log, cfg.EPGStartCommand, schedulerHookArgs(paths)); err != nil {
			return Result{}, err
		}
	}
	services, err := source.Services(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("get Mirakurun services: %w", err)
	}
	programs, err := source.Programs(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("get Mirakurun programs: %w", err)
	}
	tuners, err := source.Tuners(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("get Mirakurun tuners: %w", err)
	}
	if !simulation {
		if err := runHook(ctx, paths.Log, cfg.EPGEndCommand, schedulerHookArgs(paths)); err != nil {
			return Result{}, err
		}
	}

	schedule := BuildSchedule(cfg, services, programs)

	if !simulation {
		if err := runHook(ctx, paths.Log, cfg.SchedulerStartCommand, schedulerHookArgs(paths)); err != nil {
			return Result{}, err
		}
	}

	var rules []chinachu.Rule
	if err := storage.ReadJSON(paths.Rules, &rules, "[]"); err != nil {
		return Result{}, err
	}
	var oldReserves []chinachu.Program
	if err := storage.ReadJSON(paths.Reserves, &oldReserves, "[]"); err != nil {
		return Result{}, err
	}

	reserves, result := BuildReserves(schedule, rules, oldReserves, tuners, now)
	for _, reserve := range reserves {
		startText := legacyISODateTime(reserve.Start)
		switch {
		case reserve.IsConflict:
			if err := logging.AppendLine(paths.Log, "!CONFLICT: %s %s [%s] %s", reserve.ID, startText, reserve.Channel.Name, reserve.Title); err != nil {
				return Result{}, err
			}
			payload, err := json.Marshal(reserve)
			if err != nil {
				return Result{}, err
			}
			args := []string{
				strconv.Itoa(os.Getpid()),
				reserve.ID,
				startText,
				reserve.Channel.Name,
				reserve.Title,
				string(payload),
			}
			if err := runHook(ctx, paths.Log, cfg.ConflictCommand, args); err != nil {
				return Result{}, err
			}
		case reserve.IsSkip:
			if err := logging.AppendLine(paths.Log, "SKIP: %s %s [%s] %s", reserve.ID, startText, reserve.Channel.Name, reserve.Title); err != nil {
				return Result{}, err
			}
		case !reserve.IsSkip:
			if err := logging.AppendLine(paths.Log, "RESERVE: %s %s [%s] %s", reserve.ID, startText, reserve.Channel.Name, reserve.Title); err != nil {
				return Result{}, err
			}
		}
	}
	if err := appendResultLogs(paths.Log, result); err != nil {
		return Result{}, err
	}
	if !simulation {
		if err := storage.WriteJSONAtomic(paths.Schedule, schedule, false); err != nil {
			return Result{}, err
		}
		if err := logging.AppendLine(paths.Log, "WRITE: %s", paths.Schedule); err != nil {
			return Result{}, err
		}
		if err := storage.WriteJSONAtomic(paths.Reserves, reserves, false); err != nil {
			return Result{}, err
		}
		if err := logging.AppendLine(paths.Log, "WRITE: %s", paths.Reserves); err != nil {
			return Result{}, err
		}
		args := append(schedulerHookArgs(paths),
			strconv.Itoa(result.Matches),
			strconv.Itoa(result.Duplicates),
			strconv.Itoa(result.Conflicts),
			strconv.Itoa(result.Skips),
			strconv.Itoa(result.Reserves),
		)
		if err := runHook(ctx, paths.Log, cfg.SchedulerEndCommand, args); err != nil {
			return Result{}, err
		}
	}
	return result, nil
}

func appendResultLogs(logPath string, result Result) error {
	lines := []struct {
		name  string
		value int
	}{
		{"MATCHES", result.Matches},
		{"DUPLICATES", result.Duplicates},
		{"CONFLICTS", result.Conflicts},
		{"SKIPS", result.Skips},
		{"RESERVES", result.Reserves},
	}
	for _, line := range lines {
		if err := logging.AppendLine(logPath, "%s: %d", line.name, line.value); err != nil {
			return err
		}
	}
	return nil
}

func legacyISODateTime(timestampMS int64) string {
	return time.UnixMilli(timestampMS).In(time.Local).Format("2006-01-02T15:04:05-0700")
}

func schedulerHookArgs(paths Paths) []string {
	return []string{
		strconv.Itoa(os.Getpid()),
		paths.Rules,
		paths.Reserves,
		paths.Schedule,
	}
}

func runHook(ctx context.Context, logPath, command string, args []string) error {
	if command == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, command, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start hook %s: %w", command, err)
	}
	if err := logging.AppendLine(logPath, "SPAWN: %s (pid=%d)", command, cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("wait hook %s: %w", command, err)
	}
	return nil
}

func BuildReserves(schedule []chinachu.ChannelSchedule, rules []chinachu.Rule, oldReserves []chinachu.Program, tuners []mirakurun.Tuner, now time.Time) ([]chinachu.Program, Result) {
	matches := []chinachu.Program{}
	for _, channel := range schedule {
		for _, program := range channel.Programs {
			if chinachu.MatchesAnyRule(rules, program) {
				matches = append(matches, program)
			}
		}
	}

	for _, reserve := range oldReserves {
		if reserve.IsManualReserved {
			if reserve.Start+86400000 <= now.UnixMilli() {
				continue
			}
			if containsProgramID(matches, reserve.ID) {
				continue
			}
			if updated := chinachu.GetProgramByID(reserve.ID, schedule, nil); updated != nil {
				oneSeg := reserve.OneSeg
				reserve = *updated
				reserve.IsManualReserved = true
				reserve.OneSeg = oneSeg
			}
			matches = append(matches, reserve)
			continue
		}
		if reserve.IsSkip {
			for i := range matches {
				if matches[i].ID == reserve.ID {
					matches[i].IsSkip = true
					break
				}
			}
		}
	}

	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Start < matches[j].Start })

	duplicates := markDuplicates(matches)
	conflicts := markConflicts(matches, tuners)
	applyRecordedFormats(matches, rules)

	reserves := []chinachu.Program{}
	result := Result{Matches: len(matches), Duplicates: duplicates, Conflicts: conflicts}
	for _, program := range matches {
		if program.IsDuplicate {
			continue
		}
		reserves = append(reserves, program)
		if program.IsSkip {
			result.Skips++
		} else if !program.IsConflict {
			result.Reserves++
		}
	}
	return removeEnded(reserves, now), result
}

func markDuplicates(programs []chinachu.Program) int {
	count := 0
	for i := range programs {
		a := &programs[i]
		for j := range programs {
			b := &programs[j]
			if b.IsDuplicate || b.IsSkip {
				continue
			}
			if a.ID == b.ID || a.Channel.Type != b.Channel.Type || a.Channel.Channel != b.Channel.Channel || a.Start != b.Start || a.End != b.End || a.Title != b.Title {
				continue
			}
			if a.Channel.SID < b.Channel.SID {
				continue
			}
			a.IsDuplicate = true
			count++
			break
		}
	}
	return count
}

func markConflicts(programs []chinachu.Program, tuners []mirakurun.Tuner) int {
	threads := make([][]chinachu.Program, len(tuners))
	count := 0
	for i := range programs {
		p := &programs[i]
		if p.IsDuplicate || p.IsSkip {
			continue
		}
		p.IsConflict = true
		for tunerIndex, tuner := range tuners {
			if !stringContains(tuner.Types, p.Channel.Type) {
				continue
			}
			conflicts := false
			for _, reserved := range threads[tunerIndex] {
				if !(reserved.End <= p.Start || reserved.Start >= p.End) {
					conflicts = true
					break
				}
			}
			if conflicts {
				continue
			}
			threads[tunerIndex] = append(threads[tunerIndex], *p)
			p.IsConflict = false
			break
		}
		if p.IsConflict {
			count++
		}
	}
	return count
}

func applyRecordedFormats(programs []chinachu.Program, rules []chinachu.Rule) {
	for i := range programs {
		for _, rule := range rules {
			if rule.RecordedFormat != "" && chinachu.ProgramMatchesRule(rule, programs[i]) {
				programs[i].RecordedFormat = rule.RecordedFormat
			}
		}
	}
}

func removeEnded(programs []chinachu.Program, now time.Time) []chinachu.Program {
	out := programs[:0]
	nowMS := now.UnixMilli()
	for _, program := range programs {
		if program.End >= nowMS {
			out = append(out, program)
		}
	}
	return out
}

func containsProgramID(programs []chinachu.Program, id string) bool {
	for _, program := range programs {
		if program.ID == id {
			return true
		}
	}
	return false
}

func stringContains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
