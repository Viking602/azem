package app

import (
	"testing"

	hyprovider "github.com/Viking602/venat/provider"
)

func TestFinalAnswerTraceSeparatesCommentary(t *testing.T) {
	var trace finalAnswerTrace
	trace.append("Checking the repository. ", hyprovider.TextPhaseCommentary)
	trace.append("The change is ready.", hyprovider.TextPhaseFinalAnswer)
	trace.finishTurn()

	if got, want := trace.resolve("Checking the repository. The change is ready."), "The change is ready."; got != want {
		t.Fatalf("resolved text = %q, want %q", got, want)
	}
}

func TestFinalAnswerTraceUsesLatestTurnAndPreservesReplacements(t *testing.T) {
	var trace finalAnswerTrace
	trace.append("Reading files.", hyprovider.TextPhaseCommentary)
	trace.finishTurn()
	trace.append("Draft answer", "")
	trace.finishTurn()

	if got, want := trace.resolve("Draft answer"), "Draft answer"; got != want {
		t.Fatalf("legacy text = %q, want %q", got, want)
	}
	if got, want := trace.resolve("Guardrail replacement"), "Guardrail replacement"; got != want {
		t.Fatalf("replacement text = %q, want %q", got, want)
	}
}
