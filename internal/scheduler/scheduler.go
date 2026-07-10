package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"strata-pvr/internal/config"
	"strata-pvr/internal/database"
	legacy "strata-pvr/internal/domain"
	"strata-pvr/internal/logging"
	"strata-pvr/internal/mirakurun"
	"strata-pvr/internal/reservationstore"
	"strata-pvr/internal/rulestore"
	"strata-pvr/internal/schedulestore"
)

type Source interface {
	Services(context.Context) ([]mirakurun.Service, error)
	Programs(context.Context) ([]mirakurun.Program, error)
	Tuners(context.Context) ([]mirakurun.Tuner, error)
}

type Paths struct {
	Config   string
	Database string
	PID      string
	Log      string
}

type Result struct {
	Matches         int
	Duplicates      int
	Conflicts       int
	Skips           int
	Reserves        int
	OverridesByRule []legacy.Program
	DuplicateItems  []legacy.Program
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
	client.UserAgent = mirakurun.StrataUserAgent("scheduler")
	if paths.Database != "" {
		db, err := database.Open(ctx, paths.Database)
		if err != nil {
			return Result{}, err
		}
		defer db.Close()
		ctx = database.WithHandle(ctx, db)
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
	if err := logging.AppendLine(paths.Log, "GETTING EPG from Mirakurun."); err != nil {
		return Result{}, err
	}
	services, err := source.Services(ctx)
	if err != nil {
		logMirakurunError(paths.Log, err)
		return Result{}, fmt.Errorf("get Mirakurun services: %w", err)
	}
	if err := logging.AppendLine(paths.Log, "Mirakurun is OK."); err != nil {
		return Result{}, err
	}
	if err := logging.AppendLine(paths.Log, "Mirakurun -> services: %d", len(services)); err != nil {
		return Result{}, err
	}
	filteredServices, sortedServices := serviceLogStats(cfg, services)
	if err := logging.AppendLine(paths.Log, "Mirakurun -> services: %d (excluded)", filteredServices); err != nil {
		return Result{}, err
	}
	if err := logging.AppendLine(paths.Log, "Mirakurun -> sorted services: %d", sortedServices); err != nil {
		return Result{}, err
	}
	programs, err := source.Programs(ctx)
	if err != nil {
		logMirakurunError(paths.Log, err)
		return Result{}, fmt.Errorf("get Mirakurun programs: %w", err)
	}
	if err := logging.AppendLine(paths.Log, "Mirakurun -> programs: %d", len(programs)); err != nil {
		return Result{}, err
	}
	tuners, err := source.Tuners(ctx)
	if err != nil {
		logMirakurunError(paths.Log, err)
		return Result{}, fmt.Errorf("get Mirakurun tuners: %w", err)
	}
	if err := logging.AppendLine(paths.Log, "Mirakurun -> tuners: %d", len(tuners)); err != nil {
		return Result{}, err
	}

	schedule := BuildSchedule(cfg, services, programs)
	if err := logDuplicateIDs(paths.Log, schedule); err != nil {
		return Result{}, err
	}

	rules, err := rulestore.Read(ctx, paths.Database)
	if err != nil {
		return Result{}, err
	}
	oldReserves, err := reservationstore.Read(ctx, paths.Database)
	if err != nil {
		return Result{}, err
	}
	if err := logging.AppendLine(paths.Log, "TUNERS: %s", tunerTypesJSON(tuners)); err != nil {
		return Result{}, err
	}

	reserves, result := BuildReservesWithNormalization(schedule, rules, oldReserves, tuners, now, cfg.NormalizationForm)
	for _, reserve := range result.OverridesByRule {
		if err := logging.AppendLine(paths.Log, "OVERRIDEBYRULE: %s %s [%s] %s", reserve.ID, legacyISODateTime(reserve.Start), reserve.Channel.Name, reserve.Title); err != nil {
			return Result{}, err
		}
	}
	for _, duplicate := range result.DuplicateItems {
		if err := logging.AppendLine(paths.Log, "DUPLICATE: %s %s [%s] %s", duplicate.ID, legacyISODateTime(duplicate.Start), duplicate.Channel.Name, duplicate.Title); err != nil {
			return Result{}, err
		}
	}
	for _, reserve := range reserves {
		startText := legacyISODateTime(reserve.Start)
		switch {
		case reserve.IsConflict:
			if err := logging.AppendLine(paths.Log, "!CONFLICT: %s %s [%s] %s", reserve.ID, startText, reserve.Channel.Name, reserve.Title); err != nil {
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
		scheduleDocuments, err := schedulestore.Documents(schedule)
		if err != nil {
			return Result{}, err
		}
		reservationDocuments, err := reservationstore.Documents(reserves)
		if err != nil {
			return Result{}, err
		}
		db, release, err := database.Acquire(ctx, paths.Database)
		if err != nil {
			return Result{}, err
		}
		err = database.ReplaceSchedulerState(ctx, db, scheduleDocuments, reservationDocuments)
		release()
		if err != nil {
			return Result{}, err
		}
		if err := logging.AppendLine(paths.Log, "WRITE: %s (schedule, reserves)", paths.Database); err != nil {
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

func logMirakurunError(logPath string, err error) {
	_ = logging.AppendLine(logPath, "Mirakurun -> Error:")
	_ = logging.AppendLine(logPath, "%v", err)
}

func legacyISODateTime(timestampMS int64) string {
	return time.UnixMilli(timestampMS).In(time.Local).Format("2006-01-02T15:04:05-0700")
}

func logDuplicateIDs(logPath string, schedule []legacy.ChannelSchedule) error {
	seen := map[string]bool{}
	for _, channel := range schedule {
		for _, program := range channel.Programs {
			if seen[program.ID] {
				if err := logging.AppendLine(logPath, "**WARNING**: %s is duplicated!", program.ID); err != nil {
					return err
				}
				continue
			}
			seen[program.ID] = true
		}
	}
	return nil
}

func tunerTypesJSON(tuners []mirakurun.Tuner) string {
	counts := map[string]int{}
	order := []string{}
	for _, tuner := range tuners {
		for _, typ := range tuner.Types {
			if _, ok := counts[typ]; !ok {
				order = append(order, typ)
			}
			counts[typ]++
		}
	}

	var b strings.Builder
	b.WriteByte('{')
	for i, typ := range order {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(typ)
		b.Write(key)
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(counts[typ]))
	}
	b.WriteByte('}')
	return b.String()
}

func serviceLogStats(cfg *config.Config, services []mirakurun.Service) (filteredCount int, sortedCount int) {
	filtered := filterAndOrderServices(cfg, append([]mirakurun.Service(nil), services...))
	for _, id := range cfg.ServiceOrder {
		for _, service := range filtered {
			if service.ID == id {
				sortedCount++
				break
			}
		}
	}
	return len(filtered), sortedCount
}

func BuildReserves(schedule []legacy.ChannelSchedule, rules []legacy.Rule, oldReserves []legacy.Program, tuners []mirakurun.Tuner, now time.Time) ([]legacy.Program, Result) {
	return BuildReservesWithNormalization(schedule, rules, oldReserves, tuners, now, "")
}

func BuildReservesWithNormalization(schedule []legacy.ChannelSchedule, rules []legacy.Rule, oldReserves []legacy.Program, tuners []mirakurun.Tuner, now time.Time, normalizationForm string) ([]legacy.Program, Result) {
	matches := []legacy.Program{}
	overridesByRule := []legacy.Program{}
	for _, channel := range schedule {
		for _, program := range channel.Programs {
			if legacy.MatchesAnyRuleWithNormalization(rules, program, normalizationForm) {
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
				overridesByRule = append(overridesByRule, reserve)
				continue
			}
			if updated := legacy.GetProgramByID(reserve.ID, schedule, nil); updated != nil {
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

	duplicates, duplicateItems := markDuplicates(matches)
	conflicts := markConflicts(matches, tuners)
	applyRecordedFormats(matches, rules, normalizationForm)

	reserves := []legacy.Program{}
	result := Result{Matches: len(matches), Duplicates: duplicates, Conflicts: conflicts, OverridesByRule: overridesByRule, DuplicateItems: duplicateItems}
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

func markDuplicates(programs []legacy.Program) (int, []legacy.Program) {
	count := 0
	duplicates := []legacy.Program{}
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
			duplicates = append(duplicates, *a)
			break
		}
	}
	return count, duplicates
}

func markConflicts(programs []legacy.Program, tuners []mirakurun.Tuner) int {
	threads := make([][]legacy.Program, len(tuners))
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

func applyRecordedFormats(programs []legacy.Program, rules []legacy.Rule, normalizationForm string) {
	for i := range programs {
		for _, rule := range rules {
			if rule.RecordedFormat != "" && legacy.ProgramMatchesRuleWithNormalization(rule, programs[i], normalizationForm) {
				programs[i].RecordedFormat = rule.RecordedFormat
			}
		}
	}
}

func removeEnded(programs []legacy.Program, now time.Time) []legacy.Program {
	out := programs[:0]
	nowMS := now.UnixMilli()
	for _, program := range programs {
		if program.End >= nowMS {
			out = append(out, program)
		}
	}
	return out
}

func containsProgramID(programs []legacy.Program, id string) bool {
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
