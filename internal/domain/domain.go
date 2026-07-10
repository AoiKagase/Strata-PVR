package domain

import "strata-pvr/internal/legacy"

type Channel = legacy.Channel
type Program = legacy.Program
type ChannelSchedule = legacy.ChannelSchedule
type RangeRule = legacy.RangeRule
type DurationRule = legacy.DurationRule
type Rule = legacy.Rule

var GetProgramByID = legacy.GetProgramByID
var ProgramMatchesRule = legacy.ProgramMatchesRule
var ProgramMatchesRuleWithNormalization = legacy.ProgramMatchesRuleWithNormalization
var ProgramMatchesRuleForCLI = legacy.ProgramMatchesRuleForCLI
var ProgramMatchesRuleForCLIWithNormalization = legacy.ProgramMatchesRuleForCLIWithNormalization
var MatchesAnyRule = legacy.MatchesAnyRule
var MatchesAnyRuleWithNormalization = legacy.MatchesAnyRuleWithNormalization
var FormatRecordedName = legacy.FormatRecordedName
var StripFilename = legacy.StripFilename
