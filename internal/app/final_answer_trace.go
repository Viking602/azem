package app

import (
	"strings"

	hyprovider "github.com/Viking602/venat/provider"
)

// finalAnswerTrace separates intermediate commentary from the user-visible
// answer while preserving unphased provider text. The raw text is retained so
// output-guardrail replacements can be detected and left untouched.
type finalAnswerTrace struct {
	currentRaw     strings.Builder
	currentVisible strings.Builder
	lastRaw        string
	lastVisible    string
}

func (t *finalAnswerTrace) append(text string, phase hyprovider.TextPhase) {
	if text == "" {
		return
	}
	t.currentRaw.WriteString(text)
	if phase != hyprovider.TextPhaseCommentary {
		t.currentVisible.WriteString(text)
	}
}

func (t *finalAnswerTrace) finishTurn() {
	if t.currentRaw.Len() > 0 {
		t.lastRaw = t.currentRaw.String()
		t.lastVisible = t.currentVisible.String()
	}
	t.currentRaw.Reset()
	t.currentVisible.Reset()
}

func (t *finalAnswerTrace) resolve(resultText string) string {
	if t.lastRaw != "" && resultText == t.lastRaw {
		return t.lastVisible
	}
	return resultText
}
