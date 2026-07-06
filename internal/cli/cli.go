package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"chinachu-go/internal/chinachu"
	"chinachu-go/internal/config"
	"chinachu-go/internal/operator"
	"chinachu-go/internal/scheduler"
	"chinachu-go/internal/storage"
	"chinachu-go/internal/wui"
)

type paths struct {
	config    string
	rules     string
	schedule  string
	reserves  string
	recording string
	recorded  string
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	_ = ctx
	args, err := normalizeModeArgs(args)
	if err != nil {
		return err
	}
	p := paths{
		config:    "config.json",
		rules:     "rules.json",
		schedule:  filepath.Join("data", "schedule.json"),
		reserves:  filepath.Join("data", "reserves.json"),
		recording: filepath.Join("data", "recording.json"),
		recorded:  filepath.Join("data", "recorded.json"),
	}
	if len(args) == 0 || args[0] == "help" {
		printHelp(stdout)
		return nil
	}
	switch args[0] {
	case "installer":
		fmt.Fprintln(stdout, "Chinachu-Go installer: Node.js/npm modules are not required.")
		return nil
	case "updater":
		fmt.Fprintln(stdout, "Chinachu-Go updater: automatic git/service/installer operations are intentionally not performed.")
		fmt.Fprintln(stdout, "Update the repository and rebuild chinachu-go; Node.js/npm modules are not required.")
		return nil
	case "test":
		return testCommand(args[1:], stdout)
	case "ircbot":
		fmt.Fprintln(stdout, "Chinachu-Go ircbot: the experimental Node-era IRC bot is not implemented in the Go runtime.")
		fmt.Fprintln(stdout, "Use WUI/API or build an external bot against the Go API.")
		return nil
	case "compat":
		return compat(args[1:], stdout)
	case "service":
		return service(ctx, p, args[1:], stdout)
	case "reserve":
		return reserve(p, args[1:], stdout)
	case "unreserve":
		return updateReserve(p, args[1:], stdout, "unreserve")
	case "skip":
		return updateReserve(p, args[1:], stdout, "skip")
	case "unskip":
		return updateReserve(p, args[1:], stdout, "unskip")
	case "stop":
		return stopRecording(p, args[1:], stdout)
	case "rules":
		return ruleList(p.rules, args[1:], stdout)
	case "reserves":
		return programList(p.reserves, args[1:], stdout)
	case "recording":
		return programList(p.recording, args[1:], stdout)
	case "recorded":
		return programList(p.recorded, args[1:], stdout)
	case "cleanup":
		return cleanup(p, args[1:], stdout)
	case "update":
		return update(ctx, p, args[1:], stdout)
	case "search":
		return search(p, args[1:], stdout)
	case "rule":
		return ruleCommand(p, args[1:], stdout)
	case "enrule":
		return ruleCommand(p, ruleAliasArgs(args[1:], "--enable"), stdout)
	case "disrule":
		return ruleCommand(p, ruleAliasArgs(args[1:], "--disable"), stdout)
	case "rmrule":
		return ruleCommand(p, ruleAliasArgs(args[1:], "--remove"), stdout)
	default:
		printHelp(stdout)
		return nil
	}
}

func testCommand(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("Usage: test <app> [options]")
	}
	fmt.Fprintf(stdout, "Chinachu-Go test: usr/bin/%s is not executed by the Go runtime.\n", args[0])
	fmt.Fprintln(stdout, "Install and run external tools explicitly; Node.js/npm modules are not required.")
	return nil
}

type searchOptions struct {
	rule     chinachu.Rule
	id       string
	simple   bool
	detail   bool
	now      bool
	today    bool
	tomorrow bool
	num      int
	hasNum   bool
}

func search(p paths, args []string, stdout io.Writer) error {
	opts, err := parseSearchArgs(args)
	if err != nil {
		return err
	}
	var schedule []chinachu.ChannelSchedule
	if err := storage.ReadJSON(p.schedule, &schedule, "[]"); err != nil {
		return err
	}
	now := time.Now()
	matches := make([]chinachu.Program, 0)
	for _, channel := range schedule {
		for _, program := range channel.Programs {
			if searchMatches(opts, program, now) {
				matches = append(matches, program)
			}
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Start < matches[j].Start })
	if len(matches) == 0 {
		fmt.Fprintln(stdout, "見つかりません")
		return nil
	}
	writeProgramSearchTable(stdout, matches, opts)
	return nil
}

func programList(path string, args []string, stdout io.Writer) error {
	opts, err := parseSearchArgs(args)
	if err != nil {
		return err
	}
	var programs []chinachu.Program
	if err := storage.ReadJSON(path, &programs, "[]"); err != nil {
		return err
	}
	now := time.Now()
	matches := make([]chinachu.Program, 0, len(programs))
	for _, program := range programs {
		if searchMatches(opts, program, now) {
			matches = append(matches, program)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Start < matches[j].Start })
	if len(matches) == 0 {
		fmt.Fprintln(stdout, "見つかりません")
		return nil
	}
	writeProgramListTable(stdout, matches, opts)
	return nil
}

func parseSearchArgs(args []string) (searchOptions, error) {
	ruleOpts, rule, err := parseRuleArgs(args)
	if err != nil {
		return searchOptions{}, err
	}
	opts := searchOptions{
		rule:   rule,
		num:    ruleOpts.num,
		hasNum: ruleOpts.hasNum,
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("missing value for %s", arg)
			}
			i++
			return args[i], nil
		}
		switch arg {
		case "-id", "--id":
			v, err := value()
			if err != nil {
				return opts, err
			}
			opts.id = v
		case "-simple", "--simple":
			opts.simple = true
		case "-detail", "--detail":
			opts.detail = true
		case "-now", "--now":
			opts.now = true
		case "-today", "--today":
			opts.today = true
		case "-tomorrow", "--tomorrow":
			opts.tomorrow = true
		}
	}
	return opts, nil
}

func searchMatches(opts searchOptions, program chinachu.Program, now time.Time) bool {
	if opts.id != "" {
		return opts.id == program.ID
	}
	if !chinachu.ProgramMatchesRule(opts.rule, program) {
		return false
	}
	start := time.UnixMilli(program.Start).Local()
	end := time.UnixMilli(program.End).Local()
	if opts.now && (now.Before(start) || now.After(end)) {
		return false
	}
	if opts.today && now.Day() != start.Day() {
		return false
	}
	if opts.tomorrow && now.Day()+1 != start.Day() {
		return false
	}
	return true
}

func writeProgramSearchTable(w io.Writer, programs []chinachu.Program, opts searchOptions) {
	headers := []string{"#", "Type:CH", "Cat", "Datetime", "Dur", "Title"}
	if !opts.simple || opts.detail {
		headers = insertString(headers, 1, "Program ID")
	}
	if opts.detail {
		headers = insertString(headers, indexOfString(headers, "Cat"), "SID")
		headers = append(headers, "Description")
	}
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	for i, program := range programs {
		if opts.hasNum && i != opts.num {
			continue
		}
		datetimeLayout := "06/01/02 15:04"
		if opts.simple {
			datetimeLayout = "02 15:04"
		}
		row := []string{
			strconv.Itoa(i),
			program.Channel.Type + ":" + program.Channel.Channel,
			program.Category,
			time.UnixMilli(program.Start).Local().Format(datetimeLayout),
			fmt.Sprintf("%dm", program.Seconds/60),
			program.Title,
		}
		if !opts.simple || opts.detail {
			row = insertString(row, 1, program.ID)
		}
		if opts.detail {
			row = insertString(row, indexOfString(headers, "SID"), strconv.FormatInt(program.Channel.SID, 10))
			row = append(row, program.Detail)
		}
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
}

func writeProgramListTable(w io.Writer, programs []chinachu.Program, opts searchOptions) {
	headers := []string{"#", "Type:CH", "Cat", "By", "Datetime", "Dur", "Title"}
	if !opts.simple || opts.detail {
		headers = insertString(headers, 1, "Program ID")
	}
	if opts.detail {
		headers = insertString(headers, indexOfString(headers, "Cat"), "SID")
		headers = append(headers, "Description")
	}
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	for i, program := range programs {
		if opts.hasNum && i != opts.num {
			continue
		}
		datetimeLayout := "06/01/02 15:04"
		if opts.simple {
			datetimeLayout = "02 15:04"
		}
		row := []string{
			strconv.Itoa(i),
			program.Channel.Type + ":" + program.Channel.Channel,
			program.Category,
			reservationOwner(program),
			time.UnixMilli(program.Start).Local().Format(datetimeLayout),
			fmt.Sprintf("%dm", program.Seconds/60),
			program.Title,
		}
		if !opts.simple || opts.detail {
			row = insertString(row, 1, program.ID)
		}
		if opts.detail {
			row = insertString(row, indexOfString(headers, "SID"), strconv.FormatInt(program.Channel.SID, 10))
			row = append(row, program.Detail)
		}
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
}

func reservationOwner(program chinachu.Program) string {
	if program.IsManualReserved {
		return "user"
	}
	return "rule"
}

func insertString(values []string, index int, value string) []string {
	if index < 0 || index > len(values) {
		index = len(values)
	}
	values = append(values, "")
	copy(values[index+1:], values[index:])
	values[index] = value
	return values
}

func indexOfString(values []string, value string) int {
	for i, item := range values {
		if item == value {
			return i
		}
	}
	return -1
}

func ruleCommand(p paths, args []string, stdout io.Writer) error {
	opts, rule, err := parseRuleArgs(args)
	if err != nil {
		return err
	}
	var rules []chinachu.Rule
	if err := storage.ReadJSON(p.rules, &rules, "[]"); err != nil {
		return err
	}
	var target chinachu.Rule
	if opts.hasNum {
		if opts.num < 0 || opts.num >= len(rules) {
			return fmt.Errorf("見つかりません")
		}
		target = rules[opts.num]
	}
	mergeRule(&target, rule)
	cleanRuleDeletionMarkers(&target)
	if opts.enable {
		target.IsDisabled = false
	}
	if opts.disable {
		target.IsDisabled = true
	}
	if isZeroRule(target) && !opts.remove {
		return fmt.Errorf("ルールが空です。一つ以上の条件が必要です。")
	}
	if opts.hasNum {
		if opts.remove {
			rules = append(rules[:opts.num], rules[opts.num+1:]...)
			fmt.Fprintln(stdout, "ルールを削除します")
		} else {
			rules[opts.num] = target
			fmt.Fprintln(stdout, "Rule config:")
			writePretty(stdout, target)
		}
	} else {
		if opts.remove || opts.enable || opts.disable {
			return fmt.Errorf("見つかりません")
		}
		rules = append(rules, target)
		fmt.Fprintln(stdout, "Rule config:")
		writePretty(stdout, target)
	}
	if opts.simulation {
		return nil
	}
	return storage.WriteJSONAtomic(p.rules, rules, true)
}

func ruleList(path string, args []string, stdout io.Writer) error {
	opts, _, err := parseRuleArgs(args)
	if err != nil {
		return err
	}
	detail := hasFlag(args, "-detail", "--detail")
	var rules []chinachu.Rule
	if err := storage.ReadJSON(path, &rules, "[]"); err != nil {
		return err
	}
	keys := []string{
		"types", "categories", "channels", "ignore_channels", "reserve_flags",
		"ignore_flags", "hour", "duration", "reserve_titles", "ignore_titles",
		"reserve_descriptions", "ignore_descriptions",
	}
	headers := append([]string{"#"}, keys...)
	rows := [][]string{}
	for i, rule := range rules {
		if opts.hasNum && i != opts.num {
			continue
		}
		row := []string{strconv.Itoa(i)}
		for _, key := range keys {
			row = append(row, ruleListValue(rule, key, detail))
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		fmt.Fprintln(stdout, "見つかりません")
		return nil
	}
	if len(rows) == 1 {
		for i, header := range headers {
			fmt.Fprintf(stdout, "%s\t%s\n", header, rows[0][i])
		}
		return nil
	}
	fmt.Fprintln(stdout, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(stdout, strings.Join(row, "\t"))
	}
	return nil
}

func ruleListValue(rule chinachu.Rule, key string, detail bool) string {
	switch key {
	case "types":
		return ruleStringList(rule.Types, false)
	case "categories":
		return ruleStringList(rule.Categories, false)
	case "channels":
		return ruleStringList(rule.Channels, false)
	case "ignore_channels":
		return ruleStringList(rule.IgnoreChannels, false)
	case "reserve_flags":
		return ruleStringList(rule.ReserveFlags, false)
	case "ignore_flags":
		return ruleStringList(rule.IgnoreFlags, false)
	case "hour":
		if rule.Hour == nil {
			return "-"
		}
		return fmt.Sprintf("%d, %d", rule.Hour.Start, rule.Hour.End)
	case "duration":
		if rule.Duration == nil {
			return "-"
		}
		return fmt.Sprintf("%d, %d", rule.Duration.Min, rule.Duration.Max)
	case "reserve_titles":
		return ruleStringList(rule.ReserveTitles, !detail)
	case "ignore_titles":
		return ruleStringList(rule.IgnoreTitles, !detail)
	case "reserve_descriptions":
		return ruleStringList(rule.ReserveDescriptions, !detail)
	case "ignore_descriptions":
		return ruleStringList(rule.IgnoreDescriptions, !detail)
	default:
		return "-"
	}
}

func ruleStringList(values []string, countOnly bool) string {
	if values == nil {
		return "-"
	}
	if countOnly {
		return fmt.Sprintf("[%d]", len(values))
	}
	return strings.Join(values, ", ")
}

type ruleOptions struct {
	num        int
	hasNum     bool
	enable     bool
	disable    bool
	remove     bool
	simulation bool
}

func parseRuleArgs(args []string) (ruleOptions, chinachu.Rule, error) {
	var opts ruleOptions
	var rule chinachu.Rule
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("missing value for %s", arg)
			}
			i++
			return args[i], nil
		}
		switch arg {
		case "-s", "--simulation":
			opts.simulation = true
		case "-en", "--enable":
			opts.enable = true
		case "-dis", "--disable":
			opts.disable = true
		case "-rm", "--remove":
			opts.remove = true
		case "-n", "--num":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return opts, rule, err
			}
			opts.num = n
			opts.hasNum = true
		case "-sid", "--service-id":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			sid, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return opts, rule, err
			}
			rule.SID = sid
		case "-type", "--type":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.Types = splitCSV(v)
		case "-ch", "--channel":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.Channels = splitCSV(v)
		case "-^ch", "--ignore-channels":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.IgnoreChannels = splitCSV(v)
		case "-cat", "--category":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.Categories = splitCSV(v)
		case "-start", "--start":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			start, err := strconv.Atoi(v)
			if err != nil {
				return opts, rule, err
			}
			if rule.Hour == nil {
				rule.Hour = &chinachu.RangeRule{End: 24}
			}
			rule.Hour.Start = start
		case "-end", "--end":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			end, err := strconv.Atoi(v)
			if err != nil {
				return opts, rule, err
			}
			if rule.Hour == nil {
				rule.Hour = &chinachu.RangeRule{}
			}
			rule.Hour.End = end
		case "-mini", "--minimum":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			minimum, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return opts, rule, err
			}
			if rule.Duration == nil {
				rule.Duration = &chinachu.DurationRule{Max: 99999999, HasMax: true}
			}
			rule.Duration.Min = minimum
			rule.Duration.HasMin = true
		case "-maxi", "--maximum":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			maximum, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return opts, rule, err
			}
			if rule.Duration == nil {
				rule.Duration = &chinachu.DurationRule{HasMin: true}
			}
			rule.Duration.Max = maximum
			rule.Duration.HasMax = true
		case "-title", "--titles":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.ReserveTitles = splitCSV(v)
		case "-^title", "--ignore-titles":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.IgnoreTitles = splitCSV(v)
		case "-desc", "--descriptions":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.ReserveDescriptions = splitCSV(v)
		case "-^desc", "--ignore-descriptions":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.IgnoreDescriptions = splitCSV(v)
		case "-flag", "--flags":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.ReserveFlags = splitCSV(v)
		case "-^flag", "--ignore-flags":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.IgnoreFlags = splitCSV(v)
		}
	}
	return opts, rule, nil
}

func update(ctx context.Context, p paths, args []string, stdout io.Writer) error {
	simulation := hasFlag(args, "-s", "--simulation")
	result, err := scheduler.Run(ctx, scheduler.Paths{
		Config:   p.config,
		Rules:    p.rules,
		Schedule: p.schedule,
		Reserves: p.reserves,
		PID:      filepath.Join("data", "scheduler.pid"),
		Log:      filepath.Join("log", "scheduler"),
	}, simulation)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "RUNNING SCHEDULER.")
	fmt.Fprintf(stdout, "MATCHES: %d\n", result.Matches)
	fmt.Fprintf(stdout, "DUPLICATES: %d\n", result.Duplicates)
	fmt.Fprintf(stdout, "CONFLICTS: %d\n", result.Conflicts)
	fmt.Fprintf(stdout, "SKIPS: %d\n", result.Skips)
	fmt.Fprintf(stdout, "RESERVES: %d\n", result.Reserves)
	return nil
}

func reserve(p paths, args []string, stdout io.Writer) error {
	id, rest, err := programIDArg(args, "reserve")
	if err != nil {
		return err
	}
	simulation := hasFlag(rest, "-s", "--simulation")
	oneSeg := hasFlag(rest, "--1seg", "-1seg")
	var schedule []chinachu.ChannelSchedule
	if err := storage.ReadJSON(p.schedule, &schedule, "[]"); err != nil {
		return err
	}
	var reserves []chinachu.Program
	if err := storage.ReadJSON(p.reserves, &reserves, "[]"); err != nil {
		return err
	}
	if chinachu.GetProgramByID(id, nil, reserves) != nil {
		return fmt.Errorf("既に予約されています")
	}
	target := chinachu.GetProgramByID(id, schedule, nil)
	if target == nil {
		return fmt.Errorf("見つかりません")
	}
	target.IsManualReserved = true
	if oneSeg {
		target.OneSeg = true
	}
	reserves = append(reserves, *target)
	sort.SliceStable(reserves, func(i, j int) bool { return reserves[i].Start < reserves[j].Start })
	if simulation {
		fmt.Fprintln(stdout, "[simulation] reserve:")
		writePretty(stdout, target)
		return nil
	}
	if err := storage.WriteJSONAtomic(p.reserves, reserves, false); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "reserve:")
	writePretty(stdout, target)
	fmt.Fprintln(stdout, "予約しました。 スケジューラーを実行して競合を確認することをお勧めします")
	return nil
}

func updateReserve(p paths, args []string, stdout io.Writer, mode string) error {
	id, rest, err := programIDArg(args, mode)
	if err != nil {
		return err
	}
	simulation := hasFlag(rest, "-s", "--simulation")
	var reserves []chinachu.Program
	if err := storage.ReadJSON(p.reserves, &reserves, "[]"); err != nil {
		return err
	}
	for i := range reserves {
		if reserves[i].ID != id {
			continue
		}
		switch mode {
		case "unreserve":
			if !reserves[i].IsManualReserved {
				return fmt.Errorf("自動予約された番組は解除できません。自動予約ルールを編集してください")
			}
			target := reserves[i]
			reserves = append(reserves[:i], reserves[i+1:]...)
			if simulation {
				fmt.Fprintln(stdout, "[simulation] unreserve:")
				writePretty(stdout, target)
				return nil
			}
			if err := storage.WriteJSONAtomic(p.reserves, reserves, false); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "unreserve:")
			writePretty(stdout, target)
			fmt.Fprintln(stdout, "予約を解除しました。 ")
			return nil
		case "skip":
			if reserves[i].IsManualReserved {
				return fmt.Errorf("手動予約された番組はスキップできません。予約を解除してください。")
			}
			if reserves[i].IsSkip {
				return fmt.Errorf("既にスキップが有効になっています")
			}
			reserves[i].IsSkip = true
			target := reserves[i]
			if simulation {
				fmt.Fprintln(stdout, "[simulation] skip:")
				writePretty(stdout, target)
				return nil
			}
			if err := storage.WriteJSONAtomic(p.reserves, reserves, false); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "skip:")
			writePretty(stdout, target)
			fmt.Fprintln(stdout, "スキップを有効にしました")
			return nil
		case "unskip":
			if !reserves[i].IsSkip {
				return fmt.Errorf("既にスキップは解除されています")
			}
			reserves[i].IsSkip = false
			target := reserves[i]
			if simulation {
				fmt.Fprintln(stdout, "[simulation] skip:")
				writePretty(stdout, target)
				return nil
			}
			if err := storage.WriteJSONAtomic(p.reserves, reserves, false); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "skip:")
			writePretty(stdout, target)
			fmt.Fprintln(stdout, "スキップを解除しました")
			return nil
		}
	}
	return fmt.Errorf("見つかりません")
}

func stopRecording(p paths, args []string, stdout io.Writer) error {
	id, rest, err := programIDArg(args, "stop")
	if err != nil {
		return err
	}
	simulation := hasFlag(rest, "-s", "--simulation")
	var recording []chinachu.Program
	if err := storage.ReadJSON(p.recording, &recording, "[]"); err != nil {
		return err
	}
	for i := range recording {
		if recording[i].ID == id {
			recording[i].Abort = true
			target := recording[i]
			if simulation {
				fmt.Fprintln(stdout, "[simulation] stop:")
				writePretty(stdout, target)
				return nil
			}
			if !recording[i].IsManualReserved {
				if err := markReserveSkip(p.reserves, recording[i].ID); err != nil {
					return err
				}
			}
			if err := storage.WriteJSONAtomic(p.recording, recording, false); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "stop:")
			writePretty(stdout, target)
			fmt.Fprintln(stdout, "録画を停止しました。 ")
			return nil
		}
	}
	return fmt.Errorf("見つかりません")
}

func markReserveSkip(path, id string) error {
	var reserves []chinachu.Program
	if err := storage.ReadJSON(path, &reserves, "[]"); err != nil {
		return err
	}
	changed := false
	for i := range reserves {
		if reserves[i].ID == id {
			reserves[i].IsSkip = true
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}
	return storage.WriteJSONAtomic(path, reserves, false)
}

func cleanup(p paths, args []string, stdout io.Writer) error {
	simulation := hasFlag(args, "-s", "--simulation")
	var recorded []chinachu.Program
	if err := storage.ReadJSON(p.recorded, &recorded, "[]"); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "action\tProgram ID\tRecorded")
	kept := recorded[:0]
	removed := false
	for _, program := range recorded {
		if program.Recorded != "" {
			if _, err := os.Stat(filepath.FromSlash(program.Recorded)); err == nil {
				kept = append(kept, program)
				fmt.Fprintf(stdout, "exist\t%s\t%s\n", program.ID, program.Recorded)
				continue
			}
		}
		action := "removed"
		if simulation {
			action = "[simulation] removed"
			kept = append(kept, program)
		} else {
			removed = true
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", action, program.ID, program.Recorded)
	}
	if simulation {
		return nil
	}
	if removed {
		if _, err := storage.BackupFile(p.recorded); err != nil {
			return err
		}
	}
	return storage.WriteJSONAtomic(p.recorded, kept, false)
}

func dumpJSONFile(path, empty string, stdout io.Writer) error {
	var v any
	if err := storage.ReadJSON(path, &v, empty); err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func service(ctx context.Context, p paths, args []string, stdout io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("Usage: ./chinachu service <name> <action>")
	}
	name, action := args[0], args[1]
	if name != "operator" && name != "wui" {
		return fmt.Errorf("Usage: ./chinachu service <name> <action>")
	}
	switch action {
	case "initscript":
		fmt.Fprint(stdout, serviceInitScript(name))
		return nil
	case "execute":
		switch name {
		case "operator":
			return operator.Run(ctx, operator.Paths{
				Config:    p.config,
				Reserves:  p.reserves,
				Recording: p.recording,
				Recorded:  p.recorded,
				PID:       filepath.Join("data", "operator.pid"),
				Log:       filepath.Join("log", "operator"),
			}, 0)
		case "wui":
			return wui.Run(ctx, wui.Paths{
				Config:       p.config,
				Rules:        p.rules,
				Schedule:     p.schedule,
				Reserves:     p.reserves,
				Recording:    p.recording,
				Recorded:     p.recorded,
				WebRoot:      "web",
				LogDir:       "log",
				SchedulerPID: filepath.Join("data", "scheduler.pid"),
				OperatorPID:  filepath.Join("data", "operator.pid"),
			})
		default:
			return fmt.Errorf("Usage: ./chinachu service <name> <action>")
		}
	default:
		return fmt.Errorf("Usage: ./chinachu service <name> <action>")
	}
}

func serviceInitScript(name string) string {
	return fmt.Sprintf(`#!/bin/bash
DAEMON=./chinachu-go
DAEMON_OPTS="service %[1]s execute"
NAME=chinachu-%[1]s
PIDFILE=/var/run/${NAME}.pid

start () {
  echo -n "Starting ${NAME}: "
  if [ -f $PIDFILE ]; then
    PID=$(cat $PIDFILE)
    if ps -p $PID > /dev/null 2>&1; then
      echo "${NAME} is already running? (pid=${PID})"
      exit
    fi
    rm -f $PIDFILE
  fi
  $DAEMON $DAEMON_OPTS < /dev/null > /dev/null 2>&1 &
  PID=$!
  if [ -z $PID ]; then
    echo "Failed!"
    exit
  fi
  echo $PID > $PIDFILE
  echo "OK."
}

stop () {
  echo -n "Stopping ${NAME}: "
  if [ -f $PIDFILE ]; then
    PID=$(cat $PIDFILE)
    kill -QUIT $PID
    echo "OK."
    rm -f $PIDFILE
  else
    echo "${NAME} is not running? (${PIDFILE} not found)."
  fi
}

status () {
  if [ -f $PIDFILE ]; then
    PID=$(cat $PIDFILE)
    if ps -p $PID > /dev/null 2>&1; then
      echo "${NAME} is running."
    else
      echo "${NAME} is dead but ${PIDFILE} exists."
    fi
  else
    echo "${NAME} is NOT running."
  fi
}

case "$1" in
  start )
    start "$@"
    ;;
  stop )
    stop "$@"
    ;;
  restart )
    stop "$@"
    sleep 3
    start "$@"
    ;;
  status )
    status "$@"
    ;;
  * )
    echo "Usage: $NAME {start|stop|restart|status}" >&2
    exit 1
    ;;
esac
`, name)
}

func compat(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("Usage: chinachu-go compat <check|doctor>")
	}
	switch args[0] {
	case "check", "doctor":
		cfg, cfgErr := config.Load("config.json")
		recordedDirErr := cfgErr
		if cfgErr == nil {
			recordedDirErr = validateWritableDir(cfg.RecordedDir)
		}
		checks := []struct {
			name string
			err  error
		}{
			{"config.json", validateJSONFile("config.json", "")},
			{"rules.json", validateJSONFile("rules.json", "")},
			{"data directory", validateDir("data")},
			{"recordedDir", recordedDirErr},
			{"data/schedule.json", validateJSONFile(filepath.Join("data", "schedule.json"), "")},
			{"data/reserves.json", validateJSONFile(filepath.Join("data", "reserves.json"), "")},
			{"data/recording.json", validateJSONFile(filepath.Join("data", "recording.json"), "")},
			{"data/recorded.json", validateJSONFile(filepath.Join("data", "recorded.json"), "")},
			{"Node.js runtime", nil},
		}
		failed := false
		for _, check := range checks {
			if check.err != nil {
				failed = true
				fmt.Fprintf(stdout, "NG %s: %v\n", check.name, check.err)
			} else {
				fmt.Fprintf(stdout, "OK %s\n", check.name)
			}
		}
		if failed {
			return fmt.Errorf("compat check failed")
		}
		return nil
	default:
		return fmt.Errorf("Usage: chinachu-go compat <check|doctor>")
	}
}

func validateJSONFile(path, empty string) error {
	var v any
	return storage.ReadJSON(path, &v, empty)
}

func validateDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory")
	}
	return nil
}

func validateWritableDir(path string) error {
	if err := validateDir(path); err != nil {
		return err
	}
	f, err := os.CreateTemp(path, ".chinachu-go-compat-*")
	if err != nil {
		return err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}

func hasFlag(args []string, names ...string) bool {
	for _, arg := range args {
		for _, name := range names {
			if arg == name {
				return true
			}
		}
	}
	return false
}

func normalizeModeArgs(args []string) ([]string, error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--mode=") {
			mode := strings.TrimPrefix(arg, "--mode=")
			if mode == "" {
				return nil, fmt.Errorf("missing value for %s", arg)
			}
			out := []string{mode}
			out = append(out, args[:i]...)
			out = append(out, args[i+1:]...)
			return out, nil
		}
		if arg == "-mode" || arg == "--mode" {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("missing value for %s", arg)
			}
			mode := args[i+1]
			out := []string{mode}
			out = append(out, args[:i]...)
			out = append(out, args[i+2:]...)
			return out, nil
		}
	}
	return args, nil
}

func programIDArg(args []string, command string) (string, []string, error) {
	if len(args) == 0 {
		return "", nil, fmt.Errorf("Usage: %s <pgid>", command)
	}
	if !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:], nil
	}
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--id=") {
			id := strings.TrimPrefix(arg, "--id=")
			if id == "" {
				return "", nil, fmt.Errorf("missing value for %s", arg)
			}
			rest = append(rest, args[i+1:]...)
			return id, rest, nil
		}
		if arg == "-id" || arg == "--id" {
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("missing value for %s", arg)
			}
			id := args[i+1]
			rest = append(rest, args[:i]...)
			rest = append(rest, args[i+2:]...)
			return id, rest, nil
		}
	}
	for i, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		rest = append(rest, args[:i]...)
		rest = append(rest, args[i+1:]...)
		return arg, rest, nil
	}
	return "", nil, fmt.Errorf("Usage: %s <pgid>", command)
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func ruleAliasArgs(args []string, action string) []string {
	if len(args) == 0 {
		return []string{"-n", "", action}
	}
	out := []string{"-n", args[0], action}
	if len(args) > 1 {
		out = append(out, args[1:]...)
	}
	return out
}

func splitCSV(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := parts[:0]
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func mergeRule(dst *chinachu.Rule, src chinachu.Rule) {
	if src.SID != 0 {
		dst.SID = src.SID
	}
	if src.Types != nil {
		dst.Types = src.Types
	}
	if src.Channels != nil {
		dst.Channels = src.Channels
	}
	if src.IgnoreChannels != nil {
		dst.IgnoreChannels = src.IgnoreChannels
	}
	if src.Category != "" {
		dst.Category = src.Category
	}
	if src.Categories != nil {
		dst.Categories = src.Categories
	}
	if src.Hour != nil {
		dst.Hour = src.Hour
	}
	if src.Duration != nil {
		dst.Duration = src.Duration
	}
	if src.ReserveTitles != nil {
		dst.ReserveTitles = src.ReserveTitles
	}
	if src.IgnoreTitles != nil {
		dst.IgnoreTitles = src.IgnoreTitles
	}
	if src.ReserveDescriptions != nil {
		dst.ReserveDescriptions = src.ReserveDescriptions
	}
	if src.IgnoreDescriptions != nil {
		dst.IgnoreDescriptions = src.IgnoreDescriptions
	}
	if src.ReserveFlags != nil {
		dst.ReserveFlags = src.ReserveFlags
	}
	if src.IgnoreFlags != nil {
		dst.IgnoreFlags = src.IgnoreFlags
	}
	if src.RecordedFormat != "" {
		dst.RecordedFormat = src.RecordedFormat
	}
}

func cleanRuleDeletionMarkers(rule *chinachu.Rule) {
	if singleNull(rule.Types) {
		rule.Types = nil
	}
	if singleNull(rule.Channels) {
		rule.Channels = nil
	}
	if singleNull(rule.IgnoreChannels) {
		rule.IgnoreChannels = nil
	}
	if singleNull(rule.Categories) {
		rule.Categories = nil
	}
	if singleNull(rule.ReserveTitles) {
		rule.ReserveTitles = nil
	}
	if singleNull(rule.IgnoreTitles) {
		rule.IgnoreTitles = nil
	}
	if singleNull(rule.ReserveDescriptions) {
		rule.ReserveDescriptions = nil
	}
	if singleNull(rule.IgnoreDescriptions) {
		rule.IgnoreDescriptions = nil
	}
	if singleNull(rule.ReserveFlags) {
		rule.ReserveFlags = nil
	}
	if singleNull(rule.IgnoreFlags) {
		rule.IgnoreFlags = nil
	}
	if rule.Hour != nil && (rule.Hour.Start == -1 || rule.Hour.End == -1) {
		rule.Hour = nil
	}
	if rule.Duration != nil && (rule.Duration.Min == -1 || rule.Duration.Max == -1) {
		rule.Duration = nil
	}
	if rule.RecordedFormat == "null" {
		rule.RecordedFormat = ""
	}
}

func singleNull(values []string) bool {
	return len(values) == 1 && values[0] == "null"
}

func isZeroRule(rule chinachu.Rule) bool {
	return rule.SID == 0 &&
		len(rule.Types) == 0 &&
		len(rule.Channels) == 0 &&
		len(rule.IgnoreChannels) == 0 &&
		rule.Category == "" &&
		len(rule.Categories) == 0 &&
		rule.Hour == nil &&
		rule.Duration == nil &&
		len(rule.ReserveTitles) == 0 &&
		len(rule.IgnoreTitles) == 0 &&
		len(rule.ReserveDescriptions) == 0 &&
		len(rule.IgnoreDescriptions) == 0 &&
		len(rule.ReserveFlags) == 0 &&
		len(rule.IgnoreFlags) == 0 &&
		rule.RecordedFormat == ""
}

func writePretty(w io.Writer, v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(w, string(b))
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `
Usage: ./chinachu <cmd> ...

Commands:

installer               Run a Installer.
updater                 Run a Updater.
service <name> <action> Service-utility.

update                  Run a Scheduler.
search [options]        Search for programs.

reserve <pgid>          Reserve the program manually.
unreserve <pgid>        Unreserve the program manually.
skip <pgid>             Skip the auto-reserved program.
unskip <pgid>           Cancel the skip.
stop <pgid>             Stop the recording.

rule [options]          Add or config a rule for auto reservation.
enrule <rule#>          Enable a rule. (alias of rule -n <rule#> --enable)
disrule <rule#>         Disable a rule. (alias of rule -n <rule#> --disable)
rmrule <rule#>          Remove a rule. (alias of rule -n <rule#> --remove)

rules                   Show a list of auto reserving rules.
reserves                Show a list of reserved programs.
recording               Show a list of recording programs.
recorded                Show a list of recorded programs.

cleanup                 Clean-up the recorded list.

compat <check|doctor>   Check Chinachu-Go compatibility prerequisites.

ircbot [options]        Connect to IRC server and run a ircbot. (Experimental)

test <app> [options]    Run <app> in usr/bin

help                    Output this information.

`)
}
