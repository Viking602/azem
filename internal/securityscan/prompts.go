package securityscan

import (
	_ "embed"
	"fmt"
)

//go:embed prompts/standard.md
var StandardInstructions string

//go:embed prompts/diff.md
var DiffInstructions string

//go:embed prompts/matcher.md
var MatcherInstructions string

//go:embed prompts/reducer.md
var ReducerInstructions string

//go:embed prompts/fixer.md
var FixerInstructions string

//go:embed prompts/verifier.md
var VerifierInstructions string

func InstructionsFor(target TargetKind, worker WorkerKind) (string, error) {
	if worker == WorkerReducer {
		return ReducerInstructions, nil
	}
	if worker == WorkerMatcher {
		return MatcherInstructions, nil
	}
	if worker == WorkerFixer {
		return FixerInstructions, nil
	}
	if worker == WorkerVerifier {
		return VerifierInstructions, nil
	}
	if worker != WorkerAudit {
		return "", fmt.Errorf("security scan: no instructions for worker kind %q", worker)
	}
	if target == TargetGitRefs || target == TargetWorkingTree {
		return DiffInstructions, nil
	}
	return StandardInstructions, nil
}
