package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"chinachu-go/internal/chinachu"
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
		return service(args[1:], stdout)
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
	case "search", "rule", "enrule", "disrule", "rmrule", "update", "updater", "ircbot", "test":
		return fmt.Errorf("%s: compatibility implementation not completed", args[0])
	default:
		printHelp(stdout)
		return nil
	}
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

func service(args []string, stdout io.Writer) error {
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
		return fmt.Errorf("service %s execute: not implemented", name)
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
