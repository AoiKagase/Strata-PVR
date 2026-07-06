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

	"chinachu-go/internal/chinachu"
	"chinachu-go/internal/operator"
	"chinachu-go/internal/scheduler"
	"chinachu-go/internal/storage"
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
		return dumpJSONFile(p.rules, "[]", stdout)
	case "reserves":
		return dumpJSONFile(p.reserves, "[]", stdout)
	case "recording":
		return dumpJSONFile(p.recording, "[]", stdout)
	case "recorded":
		return dumpJSONFile(p.recorded, "[]", stdout)
	case "cleanup":
		return cleanup(p, stdout)
	case "update":
		return update(ctx, p, args[1:], stdout)
	case "rule":
		return ruleCommand(p, args[1:], stdout)
	case "enrule":
		return ruleCommand(p, ruleAliasArgs(args[1:], "--enable"), stdout)
	case "disrule":
		return ruleCommand(p, ruleAliasArgs(args[1:], "--disable"), stdout)
	case "rmrule":
		return ruleCommand(p, ruleAliasArgs(args[1:], "--remove"), stdout)
	case "search", "updater", "ircbot", "test":
		return fmt.Errorf("%s: compatibility implementation not completed", args[0])
	default:
		printHelp(stdout)
		return nil
	}
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
				rule.Duration = &chinachu.DurationRule{Max: 99999999}
			}
			rule.Duration.Min = minimum
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
				rule.Duration = &chinachu.DurationRule{}
			}
			rule.Duration.Max = maximum
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
	if len(args) == 0 {
		return fmt.Errorf("Usage: reserve <pgid>")
	}
	id := args[0]
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
	reserves = append(reserves, *target)
	sort.SliceStable(reserves, func(i, j int) bool { return reserves[i].Start < reserves[j].Start })
	if err := storage.WriteJSONAtomic(p.reserves, reserves, false); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "reserve:")
	writePretty(stdout, target)
	fmt.Fprintln(stdout, "予約しました。 スケジューラーを実行して競合を確認することをお勧めします")
	return nil
}

func updateReserve(p paths, args []string, stdout io.Writer, mode string) error {
	if len(args) == 0 {
		return fmt.Errorf("Usage: %s <pgid>", mode)
	}
	id := args[0]
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
			if err := storage.WriteJSONAtomic(p.reserves, reserves, false); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "スキップを有効にしました")
			return nil
		case "unskip":
			if !reserves[i].IsSkip {
				return fmt.Errorf("既にスキップは解除されています")
			}
			reserves[i].IsSkip = false
			if err := storage.WriteJSONAtomic(p.reserves, reserves, false); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "スキップを解除しました")
			return nil
		}
	}
	return fmt.Errorf("見つかりません")
}

func stopRecording(p paths, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("Usage: stop <pgid>")
	}
	var recording []chinachu.Program
	if err := storage.ReadJSON(p.recording, &recording, "[]"); err != nil {
		return err
	}
	for i := range recording {
		if recording[i].ID == args[0] {
			recording[i].Abort = true
			if err := storage.WriteJSONAtomic(p.recording, recording, false); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "録画を停止しました。 ")
			return nil
		}
	}
	return fmt.Errorf("見つかりません")
}

func cleanup(p paths, stdout io.Writer) error {
	var recorded []chinachu.Program
	if err := storage.ReadJSON(p.recorded, &recorded, "[]"); err != nil {
		return err
	}
	kept := recorded[:0]
	for _, program := range recorded {
		if program.Recorded != "" {
			if _, err := os.Stat(program.Recorded); err == nil {
				kept = append(kept, program)
				fmt.Fprintf(stdout, "exist\t%s\t%s\n", program.ID, program.Recorded)
			} else {
				fmt.Fprintf(stdout, "removed\t%s\t%s\n", program.ID, program.Recorded)
			}
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
		fmt.Fprintf(stdout, "#!/bin/bash\nDAEMON=./chinachu-go\nDAEMON_OPTS=\"service %s execute\"\nNAME=chinachu-%s\nPIDFILE=/var/run/${NAME}.pid\ncase \"$1\" in\n  start ) $DAEMON $DAEMON_OPTS & echo $! > $PIDFILE ;;\n  stop ) kill -QUIT $(cat $PIDFILE); rm -f $PIDFILE ;;\n  status ) test -f $PIDFILE && echo \"${NAME} is running.\" || echo \"${NAME} is NOT running.\" ;;\n  * ) echo \"Usage: $NAME {start|stop|restart|status}\" >&2; exit 1 ;;\nesac\n", name, name)
		return nil
	case "execute":
		switch name {
		case "operator":
			return operator.Run(ctx, operator.Paths{
				Config:    p.config,
				Reserves:  p.reserves,
				Recording: p.recording,
				Recorded:  p.recorded,
			}, 0)
		case "wui":
			return fmt.Errorf("service wui execute: not implemented")
		default:
			return fmt.Errorf("Usage: ./chinachu service <name> <action>")
		}
	default:
		return fmt.Errorf("Usage: ./chinachu service <name> <action>")
	}
}

func compat(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("Usage: chinachu-go compat <check|doctor>")
	}
	switch args[0] {
	case "check", "doctor":
		checks := []struct {
			name string
			err  error
		}{
			{"config.json", validateJSONFile("config.json", "{}")},
			{"rules.json", validateJSONFile("rules.json", "[]")},
			{"data directory", validateDir("data")},
			{"data/schedule.json", validateJSONFile(filepath.Join("data", "schedule.json"), "[]")},
			{"data/reserves.json", validateJSONFile(filepath.Join("data", "reserves.json"), "[]")},
			{"data/recording.json", validateJSONFile(filepath.Join("data", "recording.json"), "[]")},
			{"data/recorded.json", validateJSONFile(filepath.Join("data", "recorded.json"), "[]")},
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
	if value == "null" || value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := parts[:0]
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" && part != "null" {
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
