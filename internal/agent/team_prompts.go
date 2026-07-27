package agent

import _ "embed"

//go:embed prompts/team/planner.md
var plannerTeamInstructions string

//go:embed prompts/team/implementer.md
var implementerTeamInstructions string

//go:embed prompts/team/reviewer.md
var reviewerTeamInstructions string

//go:embed prompts/team/reporter.md
var reporterTeamInstructions string
