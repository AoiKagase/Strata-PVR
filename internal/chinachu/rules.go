package chinachu

import (
	"regexp"
	"strconv"
	"time"

	"golang.org/x/text/unicode/norm"
)

func ProgramMatchesRule(rule Rule, program Program) bool {
	return ProgramMatchesRuleWithNormalization(rule, program, "")
}

func ProgramMatchesRuleWithNormalization(rule Rule, program Program, normalizationForm string) bool {
	title := program.FullTitle
	if title == "" {
		title = program.Title
	}
	return programMatchesRule(rule, program, title, false, normalizationForm)
}

func ProgramMatchesRuleForCLI(rule Rule, program Program) bool {
	return ProgramMatchesRuleForCLIWithNormalization(rule, program, "")
}

func ProgramMatchesRuleForCLIWithNormalization(rule Rule, program Program, normalizationForm string) bool {
	return programMatchesRule(rule, program, program.Title, true, normalizationForm)
}

func programMatchesRule(rule Rule, program Program, title string, cli bool, normalizationForm string) bool {
	if rule.IsDisabled {
		return false
	}
	if rule.SID != 0 && rule.SID != program.Channel.SID {
		return false
	}
	if len(rule.Types) > 0 && !contains(rule.Types, program.Channel.Type) {
		return false
	}
	if len(rule.Channels) > 0 {
		id := program.Channel.Type + "_" + strconv.FormatInt(program.Channel.SID, 10)
		if cli {
			if !contains(rule.Channels, program.Channel.Channel) {
				return false
			}
		} else {
			if !contains(rule.Channels, program.Channel.ID) && !contains(rule.Channels, program.Channel.Channel) && !contains(rule.Channels, id) {
				return false
			}
		}
	}
	if len(rule.IgnoreChannels) > 0 {
		id := program.Channel.Type + "_" + strconv.FormatInt(program.Channel.SID, 10)
		if cli {
			if contains(rule.IgnoreChannels, program.Channel.Channel) {
				return false
			}
		} else {
			if contains(rule.IgnoreChannels, program.Channel.ID) || contains(rule.IgnoreChannels, program.Channel.Channel) || contains(rule.IgnoreChannels, id) {
				return false
			}
		}
	}
	if rule.Category != "" && rule.Category != program.Category {
		return false
	}
	if len(rule.Categories) > 0 && !contains(rule.Categories, program.Category) {
		return false
	}
	if rule.Hour != nil && !(rule.Hour.Start == 0 && rule.Hour.End == 24) {
		if !hourMatches(*rule.Hour, program.Start, program.End) {
			return false
		}
	}
	if rule.Duration != nil && rule.Duration.HasMin && rule.Duration.HasMax {
		if rule.Duration.Min > program.Seconds || rule.Duration.Max < program.Seconds {
			return false
		}
	}
	if len(rule.ReserveTitles) > 0 && !anyRegexpMatch(rule.ReserveTitles, title, normalizationForm) {
		return false
	}
	if len(rule.IgnoreTitles) > 0 && anyRegexpMatch(rule.IgnoreTitles, title, normalizationForm) {
		return false
	}
	if len(rule.ReserveDescriptions) > 0 {
		if program.Detail == "" || !anyRegexpMatch(rule.ReserveDescriptions, program.Detail, normalizationForm) {
			return false
		}
	}
	if len(rule.IgnoreDescriptions) > 0 {
		if cli && program.Detail == "" {
			return false
		}
		if program.Detail != "" && anyRegexpMatch(rule.IgnoreDescriptions, program.Detail, normalizationForm) {
			return false
		}
	}
	if len(rule.IgnoreFlags) > 0 && anyOverlap(rule.IgnoreFlags, program.Flags) {
		return false
	}
	if len(rule.ReserveFlags) > 0 {
		if cli && program.Detail == "" {
			return false
		}
		if !anyOverlap(rule.ReserveFlags, program.Flags) {
			return false
		}
	}
	return true
}

func MatchesAnyRule(rules []Rule, program Program) bool {
	return MatchesAnyRuleWithNormalization(rules, program, "")
}

func MatchesAnyRuleWithNormalization(rules []Rule, program Program, normalizationForm string) bool {
	for _, rule := range rules {
		if ProgramMatchesRuleWithNormalization(rule, program, normalizationForm) {
			return true
		}
	}
	return false
}

func hourMatches(rule RangeRule, startMS, endMS int64) bool {
	start := time.UnixMilli(startMS).Local().Hour()
	endTime := time.UnixMilli(endMS).Local()
	end := endTime.Hour()
	if start > end {
		end += 24
	}
	if endTime.Minute() == 0 {
		end--
	}
	if rule.Start > rule.End {
		return !((rule.Start > start) && (rule.End < end))
	}
	return !((rule.Start > start) || (rule.End < end))
}

func anyRegexpMatch(patterns []string, value string, normalizationForm string) bool {
	value = normalizeForRule(value, normalizationForm)
	for _, pattern := range patterns {
		pattern = normalizeForRule(pattern, normalizationForm)
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		if re.MatchString(value) {
			return true
		}
	}
	return false
}

func normalizeForRule(value string, normalizationForm string) string {
	switch normalizationForm {
	case "NFC":
		return norm.NFC.String(value)
	case "NFD":
		return norm.NFD.String(value)
	case "NFKC":
		return norm.NFKC.String(value)
	case "NFKD":
		return norm.NFKD.String(value)
	default:
		return value
	}
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func anyOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
