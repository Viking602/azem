package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"
)

const loopGuardArtifactKind = session.InternalArtifactKindPrefix + "loop-guard-v1"

type modelLoopGuard struct {
	cfg             config.LoopGuardConfig
	control         *hyagent.ControlQueue
	store           *session.Service
	sessionID       string
	runID           string
	thinking        string
	text            string
	thinkingTrips   int
	lastToolHash    string
	lastToolName    string
	toolRepeatCount int
	exemptTools     map[string]bool
}

func newModelLoopGuard(cfg config.LoopGuardConfig, control *hyagent.ControlQueue, store *session.Service, sessionID, runID string) *modelLoopGuard {
	exempt := make(map[string]bool, len(cfg.ToolCallExemptTools))
	for _, name := range cfg.ToolCallExemptTools {
		exempt[strings.ToLower(strings.TrimSpace(name))] = true
	}
	return &modelLoopGuard{cfg: cfg, control: control, store: store, sessionID: sessionID, runID: runID, exemptTools: exempt}
}

func (guard *modelLoopGuard) TransformContext(_ context.Context, messages []message.Message) ([]message.Message, error) {
	return messages, nil
}

func (guard *modelLoopGuard) BeforeModelCall(context.Context, *hyprovider.Request) error {
	guard.thinking, guard.text = "", ""
	return nil
}
func (*modelLoopGuard) BeforeToolCall(context.Context, *tool.Call) error  { return nil }
func (*modelLoopGuard) AfterToolCall(context.Context, *tool.Result) error { return nil }

func (guard *modelLoopGuard) OnEvent(ctx context.Context, event hyprovider.Event) error {
	switch event.Kind {
	case hyprovider.EventThinkingDelta:
		if !guard.cfg.ThinkingEnabled {
			return nil
		}
		guard.thinking = appendLoopBuffer(guard.thinking, event.Thinking)
		if detectGeneratedLoop(guard.thinking) {
			return guard.interrupt(ctx, "thinking-loop", "Generated reasoning is repeating without progress. Re-sample the approach, state one concrete next action, and do not repeat the stalled text.")
		}
	case hyprovider.EventTextDelta:
		if !guard.cfg.AssistantTextEnabled {
			return nil
		}
		guard.text = appendLoopBuffer(guard.text, event.Text)
		if detectGeneratedLoop(guard.text) {
			return guard.interrupt(ctx, "assistant-text-loop", "Visible answer text is repeating without progress. Re-sample and continue with new concrete information only.")
		}
	case hyprovider.EventToolCall:
		if guard.cfg.ToolCallEnabled && event.ToolCall != nil {
			return guard.observeToolCall(ctx, *event.ToolCall)
		}
	}
	return nil
}

func (guard *modelLoopGuard) observeToolCall(ctx context.Context, call message.ToolCall) error {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	if guard.exemptTools[name] {
		return nil
	}
	canonical, err := canonicalToolLoopArguments(call.Arguments)
	if err != nil {
		canonical = string(call.Arguments)
	}
	digest := sha256.Sum256([]byte(name + "\x00" + canonical))
	hash := hex.EncodeToString(digest[:])
	if hash == guard.lastToolHash {
		guard.toolRepeatCount++
	} else {
		guard.lastToolHash, guard.lastToolName, guard.toolRepeatCount = hash, call.Name, 1
	}
	if guard.toolRepeatCount < guard.cfg.ToolCallThreshold {
		return nil
	}
	guard.lastToolHash, guard.toolRepeatCount = "", 0
	return guard.interrupt(ctx, "tool-call-loop", fmt.Sprintf("The model requested the identical %s call %d consecutive times. Inspect the prior result, change strategy or arguments, and do not repeat the same call.", call.Name, guard.cfg.ToolCallThreshold))
}

func (guard *modelLoopGuard) interrupt(ctx context.Context, kind, guidance string) error {
	guard.thinkingTrips++
	if guard.thinkingTrips > 3 {
		return fmt.Errorf("%s persisted after three guarded retries", kind)
	}
	if guard.control == nil {
		return errors.New("loop guard has no turn-control channel")
	}
	id, err := randomID("loop")
	if err != nil {
		return err
	}
	value := message.NewText(message.RoleSystem, "[Host loop guard: "+kind+"]\n"+guidance)
	value.Visibility = message.VisibilityPrivate
	if guard.store != nil {
		payload, _ := json.Marshal(map[string]any{"version": 1, "kind": kind, "attempt": guard.thinkingTrips, "controlId": id})
		if _, err := guard.store.PutArtifact(ctx, guard.sessionID, guard.runID, loopGuardArtifactKind, payload, ""); err != nil {
			return err
		}
	}
	if err := guard.control.Enqueue(hyagent.ControlMessage{ID: id, Kind: hyagent.ControlSteer, Message: value}); err != nil {
		return err
	}
	return &hyagent.StreamRuleInterruptError{Reason: kind, KeepPartial: false}
}

func appendLoopBuffer(current, delta string) string {
	current += delta
	if len(current) > 128<<10 {
		current = current[len(current)-(128<<10):]
	}
	return current
}

var loopParagraphSeparator = regexp.MustCompile(`\n\s*\n`)

func detectGeneratedLoop(text string) bool {
	if len(text) < 1024 {
		return false
	}
	if len(text)%2 == 0 {
		half := len(text) / 2
		if text[:half] == text[half:] && len(strings.Fields(text[half:])) >= 20 {
			return true
		}
	}
	for _, width := range []int{256, 512, 1024, 2048} {
		if len(text) >= width*2 {
			left, right := text[len(text)-width*2:len(text)-width], text[len(text)-width:]
			if left == right && len(strings.Fields(right)) >= 20 {
				return true
			}
		}
	}
	paragraphs := splitLoopParagraphs(text)
	if len(paragraphs) < 4 {
		return false
	}
	last := paragraphs[len(paragraphs)-1]
	duplicates := 0
	for _, prior := range paragraphs[max(0, len(paragraphs)-5) : len(paragraphs)-1] {
		if loopSimilarity(last, prior) >= 0.92 {
			duplicates++
		}
	}
	return duplicates >= 2
}

func splitLoopParagraphs(text string) []string {
	raw := loopParagraphSeparator.Split(text, -1)
	result := make([]string, 0, len(raw))
	for _, paragraph := range raw {
		normalized := normalizeLoopText(paragraph)
		if len(strings.Fields(normalized)) >= 20 {
			result = append(result, normalized)
		}
	}
	return result
}

func normalizeLoopText(text string) string {
	var output strings.Builder
	space := false
	for _, current := range strings.ToLower(text) {
		if unicode.IsLetter(current) || unicode.IsNumber(current) || current == '_' {
			if space && output.Len() > 0 {
				output.WriteByte(' ')
			}
			space = false
			output.WriteRune(current)
		} else {
			space = true
		}
	}
	return output.String()
}

func loopSimilarity(left, right string) float64 {
	leftWords, rightWords := strings.Fields(left), strings.Fields(right)
	if len(leftWords) == 0 || len(rightWords) == 0 {
		return 0
	}
	leftSet, rightSet := make(map[string]bool), make(map[string]bool)
	for _, word := range leftWords {
		leftSet[word] = true
	}
	for _, word := range rightWords {
		rightSet[word] = true
	}
	intersection := 0
	for word := range leftSet {
		if rightSet[word] {
			intersection++
		}
	}
	union := len(leftSet) + len(rightSet) - intersection
	return float64(intersection) / float64(max(1, union))
}

func canonicalToolLoopArguments(raw json.RawMessage) (string, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	removeLoopIntentFields(value)
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func removeLoopIntentFields(value any) {
	switch current := value.(type) {
	case map[string]any:
		for key, nested := range current {
			normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
			if normalized == "intent" || normalized == "toolintent" {
				delete(current, key)
				continue
			}
			removeLoopIntentFields(nested)
		}
	case []any:
		for _, nested := range current {
			removeLoopIntentFields(nested)
		}
	}
}

type unexpectedStopGuard struct {
	mode       string
	maxRetries int
	retries    int
}

func newUnexpectedStopGuard(cfg config.LoopGuardConfig) hyagent.OutputGuardrail {
	if cfg.UnexpectedStop == "none" {
		return nil
	}
	guard := &unexpectedStopGuard{mode: cfg.UnexpectedStop, maxRetries: cfg.UnexpectedStopRetries}
	return hyagent.NewOutputGuardrail("unexpected-stop", guard.check)
}

func (guard *unexpectedStopGuard) check(_ context.Context, input hyagent.OutputGuardrailInput) (hyagent.OutputGuardrailResult, error) {
	if !isUnexpectedStopCandidate(input.Output) {
		return hyagent.AllowOutput(), nil
	}
	if guard.retries >= guard.maxRetries {
		return hyagent.BlockOutput("provider repeatedly stopped before producing a complete answer"), nil
	}
	guard.retries++
	value := message.NewText(message.RoleSystem, "The provider stopped unexpectedly with an empty or mechanically incomplete answer. Continue from the exact unfinished point. Do not restart completed work, and finish the requested result before ending.")
	value.Visibility = message.VisibilityPrivate
	return hyagent.RetryOutput(value), nil
}

func isUnexpectedStopCandidate(output message.Message) bool {
	text := strings.TrimSpace(output.Text)
	if text == "" {
		return strings.TrimSpace(output.Thinking) != "" || len(output.ToolCalls) == 0
	}
	if strings.Count(text, "```")%2 != 0 {
		return true
	}
	lower := strings.ToLower(text)
	for _, suffix := range []string{"i will", "i'll", " next i", " next,", " therefore,", " because", " and then", " let me"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	last := text[len(text)-1]
	return last == ',' || last == ';' || last == '(' || last == '[' || last == '{'
}
