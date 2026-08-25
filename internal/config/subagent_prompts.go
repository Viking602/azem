package config

import _ "embed"

//go:embed prompts/subagents/worker.md
var workerSubagentInstructions string

//go:embed prompts/subagents/explore.md
var exploreSubagentInstructions string

//go:embed prompts/subagents/plan.md
var planSubagentInstructions string

//go:embed prompts/subagents/review.md
var reviewSubagentInstructions string

//go:embed prompts/subagents/verify.md
var verifySubagentInstructions string

//go:embed prompts/subagents/security-baseline.md
var securityBaselineSubagentInstructions string

//go:embed prompts/subagents/security-investigator.md
var securityInvestigatorSubagentInstructions string
