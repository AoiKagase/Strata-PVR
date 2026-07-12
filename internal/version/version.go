package version

import (
	"runtime/debug"
	"strings"
)

// These values are replaced by the release build scripts. Keeping useful
// defaults here also makes ad-hoc `go build` binaries identifiable.
var (
	Number = "0.1.0-dev"
	Commit = "unknown"
	Date   = "unknown"
)

type Details struct {
	Number   string
	Commit   string
	Date     string
	Modified bool
}

func Current() Details {
	details := Details{Number: Number, Commit: Commit, Date: Date}
	if build, ok := debug.ReadBuildInfo(); ok {
		settings := make(map[string]string, len(build.Settings))
		for _, setting := range build.Settings {
			settings[setting.Key] = setting.Value
		}
		if details.Commit == "unknown" && settings["vcs.revision"] != "" {
			details.Commit = shortCommit(settings["vcs.revision"])
		}
		if details.Date == "unknown" && settings["vcs.time"] != "" {
			details.Date = settings["vcs.time"]
		}
		details.Modified = settings["vcs.modified"] == "true"
	}
	if details.Commit != "unknown" && Number == "0.1.0-dev" {
		details.Number += "+" + details.Commit
	}
	return details
}

func shortCommit(commit string) string {
	commit = strings.TrimSpace(commit)
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
