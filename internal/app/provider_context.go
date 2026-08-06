package app

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/api"
	"github.com/Viking602/venat/message"
)

//go:embed prompts/main.md
var mainInstructions string

//go:embed prompts/plan.md
var planModeInstructions string

const (
	failedAssistantLabel = "[Incomplete assistant output from a failed attempt; treat it as uncommitted work.]\n"
)

var mainInstructionFingerprint = func() string {
	sum := sha256.Sum256([]byte(mainInstructions))
	return hex.EncodeToString(sum[:])
}()

func turnInstructions(planMode bool) (string, string) {
	instructions := mainInstructions
	if planMode {
		instructions += "\n\n" + planModeInstructions
	}
	sum := sha256.Sum256([]byte(instructions))
	return instructions, hex.EncodeToString(sum[:])
}

type TurnRequest struct {
	SessionID          string
	Prompt             string
	Provider           string
	Model              string
	History            []session.Block
	Reasoning          string
	AgentMode          string
	PlanMode           bool
	DisableSubagents   bool
	ActiveSkills       []string
	Images             []session.Attachment
	Todo               session.TodoList
	privateContext     string
	accountID          string
	historicalContext  string
	resuming           bool
	budgetRestored     bool
	maxTokens          int64
	maxToolCalls       int
	maxWallClock       time.Duration
	startedAt          time.Time
	usedTokens         int64
	usedToolCalls      int
	modelHistory       session.ModelHistory
	toolRecords        []session.ToolRecord
	checkpointBoundary *int64
	immutableIdentity  string
}

type turnContext struct {
	sessionID                 string
	instructions              string
	instructionFingerprint    string
	providerID                string
	modelID                   string
	runID                     string
	privateContext            string
	historicalContext         string
	resuming                  bool
	history                   []session.Block
	modelHistory              session.ModelHistory
	toolRecords               []session.ToolRecord
	workspaceRoot             string
	images                    []session.Attachment
	checkpointBoundary        *int64
	reportContextTokens       func(context.Context, int)
	compactHooks              func(context.Context, []message.Message, []message.Message, error) error
	summarize                 func(context.Context, string) (string, error)
	putArtifact               func(context.Context, string, []byte, string) (session.ContextArtifact, error)
	largeToolTokens           int
	compactTargetTokens       int
	minReclaimTokens          int
	resolveSummarizer         func(context.Context) (func(context.Context, string) (string, error), int, error)
	structuredSummary         bool
	todo                      session.TodoList
	loadTodo                  func(context.Context) (session.TodoList, error)
	softTriggerTokens         int
	backgroundPrepare         bool
	staticIdentity            string
	coordinator               *compactionCoordinator
	activateCompaction        func(context.Context, []message.Message, string) error
	reportCachePrefixDegraded func(reason string)
	semanticCheckpoint        session.SemanticCheckpointV1
	subagentFinishedAtNS      int64
	subagentID                string
}

// compactionCoordinator is deliberately in-memory: a prepared summary is only
// an optimization. After a crash the durable active checkpoint and canonical
// tail remain authoritative and the next hard trigger compacts synchronously.
type compactionCoordinator struct {
	mu        sync.Mutex
	hash      string
	source    []message.Message
	done      chan struct{}
	cancel    context.CancelFunc
	result    []message.Message
	err       error
	activated string
}

func compactionSourceHash(history []message.Message, target int, static string) string {
	normalized := append([]message.Message(nil), history...)
	for i := range normalized {
		normalized[i].CreatedAt = time.Time{}
	}
	payload, _ := json.Marshal(struct {
		Messages []message.Message `json:"messages"`
		Target   int               `json:"target"`
		Static   string            `json:"static"`
		Wire     int               `json:"wire"`
	}{normalized, target, static, session.CurrentWireVersion})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func compactionSummaryHash(history []message.Message) string {
	return session.ModelCheckpointHash(history)
}

func activeCacheIdentity(staticIdentity, manifestHash, summaryHash string) string {
	digest := sha256.Sum256([]byte(staticIdentity + "\x00" + manifestHash + "\x00" + summaryHash))
	return hex.EncodeToString(digest[:])
}

func (c turnContext) activateCompactionResult(ctx context.Context, result []message.Message) ([]message.Message, error) {
	if c.activateCompaction == nil {
		return result, nil
	}
	_, manifest := extractContextCheckpoint(result)
	manifestHash := ""
	if manifest != nil {
		manifestHash = manifest.ManifestHash
	}
	identity := activeCacheIdentity(c.staticIdentity, manifestHash, compactionSummaryHash(result))
	if c.coordinator == nil {
		return result, c.activateCompaction(ctx, result, identity)
	}
	c.coordinator.mu.Lock()
	defer c.coordinator.mu.Unlock()
	if c.coordinator.activated == identity {
		return result, nil
	}
	if err := c.activateCompaction(ctx, result, identity); err != nil {
		return result, err
	}
	c.coordinator.activated = identity
	return result, nil
}

func compactionSourcePrefix(source, current []message.Message) bool {
	if len(source) > len(current) {
		return false
	}
	for index := range source {
		left, right := source[index], current[index]
		left.CreatedAt, right.CreatedAt = time.Time{}, time.Time{}
		if !reflect.DeepEqual(left, right) {
			return false
		}
	}
	return true
}

func preparedWithUncoveredTail(prepared, source, current []message.Message, target int) ([]message.Message, bool) {
	if len(prepared) == 0 || !compactionSourcePrefix(source, current) {
		return nil, false
	}
	result := append(append([]message.Message(nil), prepared...), current[len(source):]...)
	if reflect.DeepEqual(result, current) || (target > 0 && estimateContextTokens(result) > target) {
		return nil, false
	}
	if err := message.ValidateCompleteTurns(result); err != nil {
		return nil, false
	}
	return result, true
}

func (c turnContext) Build(ctx context.Context, task api.Task) ([]message.Message, error) {
	saved := c.modelHistory
	fingerprint := c.instructionFingerprint
	if fingerprint == "" {
		fingerprint = mainInstructionFingerprint
	}
	staticPrefixCompatible := saved.StaticPrefixHash == fingerprint ||
		(c.staticIdentity != "" && saved.StaticPrefixHash == c.staticIdentity)
	compatible := len(saved.Messages) > 0 &&
		saved.ProviderID == c.providerID &&
		saved.ModelID == c.modelID &&
		saved.InstructionFingerprint == fingerprint &&
		staticPrefixCompatible &&
		saved.WireVersion == session.CurrentWireVersion &&
		saved.CoveredThroughSequence != nil && c.checkpointBoundary != nil &&
		*saved.CoveredThroughSequence == *c.checkpointBoundary
	messages := make([]message.Message, 0, len(saved.Messages)+len(c.history)+6)
	if compatible {
		messages = append(messages, saved.Messages...)
	} else {
		if modelHistoryHasProviderState(saved.Messages) && c.reportCachePrefixDegraded != nil {
			// Fallback rebuilds from transcript blocks drop encrypted reasoning /
			// ProviderState, which is the top cause of prompt-cache misses on
			// reasoning models (especially xAI). Surface the degradation once.
			c.reportCachePrefixDegraded("model history incompatible; rebuilding without provider state may reduce prompt-cache hits")
		}
		if c.instructions != "" {
			messages = append(messages, message.NewText(message.RoleSystem, c.instructions))
		}
		for _, block := range c.history {
			if value, ok := blockMessage(block); ok {
				messages = append(messages, value)
			}
		}
	}
	messages = append(messages, c.toolContinuityMessages(ctx)...)
	if text := strings.TrimSpace(c.privateContext); text != "" {
		value := message.NewText(message.RoleSystem, "[Trusted private hook context]\n"+text)
		value.Visibility = message.VisibilityPrivate
		messages = append(messages, value)
	}
	todo, err := c.currentTodo(ctx)
	if err != nil {
		return nil, err
	}
	if reminder := todoReminder(todo); reminder != "" {
		messages = append(messages, c.todoReminderMessage(reminder))
	}
	historical := strings.TrimSpace(c.historicalContext)
	if historical != "" {
		policy := message.NewText(message.RoleSystem, historicalEvidencePolicy)
		policy.Visibility = message.VisibilityPrivate
		messages = append(messages, policy)
	}
	if compatible {
		for _, block := range c.history {
			if block.Sequence > *c.checkpointBoundary {
				if value, ok := blockMessage(block); ok {
					messages = append(messages, value)
				}
			}
		}
	}
	if historical != "" {
		data := message.NewText(message.RoleUser, "<historical-evidence-json>\n"+historical+"\n</historical-evidence-json>")
		data.Visibility = message.VisibilityPrivate
		messages = append(messages, data)
	}
	goal := strings.TrimSpace(task.Goal)
	images := c.images
	if c.resuming {
		for _, block := range c.history {
			if block.RunID == c.runID && block.Kind == "user" {
				goal = ""
				images = nil
				break
			}
		}
	}
	if goal != "" || len(images) > 0 {
		messages = append(messages, UserMessageWithAttachments(goal, images))
	}
	return messages, nil
}

func modelHistoryHasProviderState(messages []message.Message) bool {
	for _, current := range messages {
		if len(current.ProviderState) > 0 {
			return true
		}
	}
	return false
}

func blockMessage(block session.Block) (message.Message, bool) {
	text := strings.TrimSpace(block.Content)
	if text == "" && len(block.Attachments) == 0 {
		return message.Message{}, false
	}
	if block.Kind == "user" {
		value := UserMessageWithAttachments(text, block.Attachments)
		value.Metadata = copyMessageMetadata(value.Metadata, block.Sequence)
		return value, true
	}
	if block.Kind != "assistant" || text == "" {
		return message.Message{}, false
	}
	if block.State == "failed" {
		text = failedAssistantLabel + text
	}
	value := message.NewText(message.RoleAssistant, text)
	value.Metadata = copyMessageMetadata(value.Metadata, block.Sequence)
	return value, true
}

const sourceSequenceMetadataKey = "azem.context.source_sequence"

func copyMessageMetadata(metadata map[string]string, sequence int64) map[string]string {
	result := make(map[string]string, len(metadata)+1)
	for key, value := range metadata {
		result[key] = value
	}
	result[sourceSequenceMetadataKey] = fmt.Sprint(sequence)
	return result
}

const (
	todoReminderPrefix         = "[Session Todo private reminder]"
	todoReminderRunMetadataKey = "azem.todo.run_id"
	todoReminderCleared        = "state=cleared. This update supersedes all earlier todo reminders for this run."
)

func (c turnContext) todoReminderMessage(reminder string) message.Message {
	value := message.NewText(message.RoleSystem, reminder)
	return c.tagTodoReminder(value)
}

func (c turnContext) tagTodoReminder(value message.Message) message.Message {
	value.Visibility = message.VisibilityPrivate
	if c.runID != "" {
		value.Metadata = map[string]string{todoReminderRunMetadataKey: c.runID}
	}
	return value
}

func (c turnContext) currentTodo(ctx context.Context) (session.TodoList, error) {
	if c.loadTodo != nil {
		return c.loadTodo(ctx)
	}
	return c.todo.Clone(), nil
}

func todoReminder(todo session.TodoList) string {
	if strings.TrimSpace(todo.Goal) == "" && len(todo.Phases) == 0 {
		return ""
	}
	var open []string
	closed := 0
	for _, phase := range todo.Phases {
		for _, item := range phase.Items {
			switch item.Status {
			case session.TodoPending, session.TodoInProgress:
				open = append(open, fmt.Sprintf("%s:%s:%s", item.ID, item.Status, item.Content))
			default:
				closed++
			}
		}
	}
	return fmt.Sprintf("%s goal=%q revision=%d open=[%s] closed=%d. Use the todo tool with expected_revision for updates.", todoReminderPrefix, todo.Goal, todo.Revision, strings.Join(open, "; "), closed)
}

func (c turnContext) refreshTodoReminder(ctx context.Context, history []message.Message) ([]message.Message, error) {
	todo, err := c.currentTodo(ctx)
	if err != nil {
		return nil, err
	}
	target := -1
	for index, current := range history {
		if current.Role != message.RoleSystem || current.Visibility != message.VisibilityPrivate || !strings.HasPrefix(current.Text, todoReminderPrefix) {
			continue
		}
		if c.runID == "" || current.Metadata[todoReminderRunMetadataKey] == c.runID {
			target = index
		}
	}
	reminder := todoReminder(todo)
	if target < 0 {
		if reminder == "" {
			return history, nil
		}
		return append(append([]message.Message(nil), history...), c.todoReminderMessage(reminder)), nil
	}
	if reminder == "" {
		reminder = fmt.Sprintf("%s revision=%d %s", todoReminderPrefix, todo.Revision, todoReminderCleared)
	}
	if history[target].Text == reminder {
		return history, nil
	}
	// Provider prompt caches require an exact prefix. Never replace or remove a
	// reminder that may already have been sent. Private system messages are
	// serialized as developer input at their current position, so appending the
	// update preserves the complete wire prefix and its trusted semantics.
	return append(append([]message.Message(nil), history...), c.todoReminderMessage(reminder)), nil
}

func (c turnContext) Compact(ctx context.Context, history []message.Message) (result []message.Message, resultErr error) {
	target := c.compactTargetTokens
	if target <= 0 {
		target = max(512, estimateContextTokens(history)*3/4)
	}
	result, resultErr = c.prepareCompactionReason(ctx, history, target, "automatic_hard")
	if resultErr == nil && !reflect.DeepEqual(result, history) {
		result, resultErr = c.activateCompactionResult(ctx, result)
	}
	return result, resultErr
}

func (c turnContext) compactRequired(ctx context.Context, history []message.Message, targetTokens int) (result []message.Message, resultErr error) {
	original := history
	history, err := c.refreshTodoReminder(ctx, history)
	if err != nil {
		return original, err
	}
	beforeTokens := estimateContextTokens(history)
	report := func(prepared []message.Message) []message.Message {
		if c.reportContextTokens != nil {
			c.reportContextTokens(ctx, estimateContextTokens(prepared))
		}
		return prepared
	}
	if targetTokens <= 0 {
		return report(history), nil
	}
	if err := message.ValidateCompleteTurns(history); err != nil {
		return history, err
	}
	if beforeTokens <= targetTokens {
		return report(history), nil
	}
	return c.prepareCompaction(ctx, history, targetTokens)
}

// prepareCompaction prepares a checkpoint toward the absolute configured
// target. Unlike compactRequired it is intentionally forced: soft-triggered
// background work calls it while the source still fits below the hard limit.
// hardTriggerTokens is retained separately for mandatory-tail validation.
func (c turnContext) prepareCompaction(ctx context.Context, history []message.Message, hardTriggerTokens int) (result []message.Message, resultErr error) {
	return c.prepareCompactionReason(ctx, history, hardTriggerTokens, "automatic_hard")
}

func (c turnContext) prepareCompactionReason(ctx context.Context, history []message.Message, hardTriggerTokens int, reason string) (result []message.Message, resultErr error) {
	original := history
	targetTokens := hardTriggerTokens
	report := func(prepared []message.Message) []message.Message {
		if c.reportContextTokens != nil {
			c.reportContextTokens(ctx, estimateContextTokens(prepared))
		}
		return prepared
	}
	history, err := c.normalizeToolResults(ctx, history)
	if err != nil {
		return original, err
	}
	beforeTokens := estimateContextTokens(history)
	if c.compactTargetTokens > 0 {
		targetTokens = c.compactTargetTokens
	}
	if c.minReclaimTokens > 0 && beforeTokens > c.minReclaimTokens && beforeTokens-targetTokens < c.minReclaimTokens {
		targetTokens = beforeTokens - c.minReclaimTokens
	}
	if c.summarize == nil && c.resolveSummarizer == nil {
		return original, fmt.Errorf("compact context: compaction model is unavailable")
	}
	previousStates := make([]string, 0, 1)
	if c.semanticCheckpoint.Revision > 0 && len(c.semanticCheckpoint.State) > 0 {
		previousStates = append(previousStates, string(c.semanticCheckpoint.State))
	}
	withoutSummaries := make([]message.Message, 0, len(history))
	for _, current := range history {
		if current.Kind == message.KindCompactionSummary {
			if len(previousStates) == 0 {
				previousStates = append(previousStates, strings.TrimSpace(strings.TrimPrefix(current.Text, semanticStateSafetyLabel)))
			}
			continue
		}
		withoutSummaries = append(withoutSummaries, current)
	}
	history = withoutSummaries
	prefixEnd := 0
	for prefixEnd < len(history) && history[prefixEnd].Role == message.RoleSystem {
		prefixEnd++
	}
	recentUsers := recentUserIndexes(history, prefixEnd, contextRecentUserTurns)
	if len(recentUsers) == 0 {
		return original, fmt.Errorf("compact context: no user turn can be preserved")
	}
	selectedUsers := make(map[int]struct{}, len(recentUsers))
	mandatory := append([]message.Message(nil), history[:prefixEnd]...)
	for _, index := range recentUsers {
		selectedUsers[index] = struct{}{}
		mandatory = append(mandatory, history[index])
	}
	mandatoryTokens := estimateContextTokens(mandatory)
	if hardTriggerTokens > 0 && mandatoryTokens > hardTriggerTokens {
		return original, fmt.Errorf("compact context: mandatory recent user evidence requires %d tokens but hard limit allows %d", mandatoryTokens, hardTriggerTokens)
	}
	latestUser := recentUsers[len(recentUsers)-1]
	tailGroups, groupErr := compactionAtomicGroups(history[latestUser+1:])
	if groupErr != nil {
		return original, groupErr
	}
	tailStarts := make([]int, 1, len(tailGroups)+1)
	tailStarts[0] = latestUser + 1
	for _, group := range tailGroups {
		tailStarts = append(tailStarts, latestUser+1+group.end)
	}
	hooksStarted := false
	rollingToolTurn := false
	for _, current := range history[latestUser+1:] {
		if len(current.ToolCalls) > 0 {
			rollingToolTurn = true
			break
		}
	}
	for _, hotStart := range tailStarts {
		omitted := make([]message.Message, 0, hotStart-prefixEnd)
		for index := prefixEnd; index < hotStart; index++ {
			if _, preserved := selectedUsers[index]; !preserved {
				omitted = append(omitted, history[index])
			}
		}
		if len(omitted) == 0 && len(previousStates) == 0 {
			continue
		}
		base := make([]message.Message, 0, prefixEnd+len(recentUsers)+len(history)-hotStart)
		base = append(base, history[:prefixEnd]...)
		for _, index := range recentUsers {
			base = append(base, history[index])
		}
		base = append(base, history[hotStart:]...)
		if estimateContextTokens(base) > targetTokens {
			continue
		}
		if !hooksStarted && c.compactHooks != nil {
			if hookErr := c.compactHooks(ctx, history, nil, nil); hookErr != nil {
				return original, hookErr
			}
			hooksStarted = true
			defer func() { _ = c.compactHooks(ctx, original, result, resultErr) }()
		}
		envelope, summaryErr := c.summarizeStateBounded(ctx, previousStates, omitted)
		if summaryErr != nil {
			return original, fmt.Errorf("rebuild semantic state: %w", summaryErr)
		}
		generated := strings.TrimSpace(envelope.Body)
		if generated == "" {
			return original, fmt.Errorf("rebuild semantic state: empty state")
		}
		summary := message.NewText(message.RoleAssistant, semanticStateSafetyLabel+generated)
		summary.Kind = message.KindCompactionSummary
		summary.Visibility = message.VisibilityPrivate
		summary.CreatedAt = time.Time{}
		compacted := make([]message.Message, 0, len(base)+1)
		compacted = append(compacted, history[:prefixEnd]...)
		if rollingToolTurn {
			for _, index := range recentUsers {
				compacted = append(compacted, history[index])
			}
			compacted = append(compacted, summary)
		} else {
			compacted = append(compacted, summary)
			for _, index := range recentUsers {
				compacted = append(compacted, history[index])
			}
		}
		compacted = append(compacted, history[hotStart:]...)
		compacted, summaryErr = c.refreshTodoReminder(ctx, compacted)
		if summaryErr != nil {
			return original, summaryErr
		}
		if estimateContextTokens(compacted) <= targetTokens {
			if validationErr := message.ValidateCompleteTurns(compacted); validationErr != nil {
				return original, validationErr
			}
			metadata, metadataErr := buildContextCheckpointMetadata(c, reason, history, compacted, generated, envelope.Authorities, targetTokens)
			if metadataErr != nil {
				return original, metadataErr
			}
			for index := range compacted {
				if compacted[index].Kind == message.KindCompactionSummary {
					compacted[index], metadataErr = attachContextCheckpoint(compacted[index], metadata)
					break
				}
			}
			if metadataErr != nil {
				return original, metadataErr
			}
			return report(compacted), nil
		}
	}
	return original, fmt.Errorf("compact context: required messages exceed %d-token target", targetTokens)
}

func (c turnContext) CompactTo(ctx context.Context, history []message.Message, hardTokens int) ([]message.Message, error) {
	normalized, err := c.normalizeToolResults(ctx, history)
	if err != nil {
		return history, err
	}
	history = normalized
	// Contexts without a background coordinator still use the same rebuild
	// kernel, synchronously.
	if c.softTriggerTokens <= 0 || c.coordinator == nil {
		result, err := c.compactRequired(ctx, history, hardTokens)
		if err == nil && !reflect.DeepEqual(result, history) {
			result, err = c.activateCompactionResult(ctx, result)
		}
		return result, err
	}
	refreshed, err := c.refreshTodoReminder(ctx, history)
	if err != nil {
		return history, err
	}
	tokens := estimateContextTokens(refreshed)
	report := func(result []message.Message) []message.Message {
		if c.reportContextTokens != nil {
			c.reportContextTokens(ctx, estimateContextTokens(result))
		}
		return result
	}
	if tokens < c.softTriggerTokens {
		return report(refreshed), nil
	}
	hash := compactionSourceHash(refreshed, c.compactTargetTokens, c.staticIdentity)
	coord := c.coordinator
	coord.mu.Lock()
	compatiblePreparation := coord.done != nil && compactionSourcePrefix(coord.source, refreshed)
	preparedReady := false
	if compatiblePreparation {
		select {
		case <-coord.done:
			preparedReady = true
		default:
		}
	}
	if coord.hash != hash && !compatiblePreparation && coord.cancel != nil {
		coord.cancel()
	}
	if tokens < hardTokens && !preparedReady {
		if !c.backgroundPrepare {
			coord.mu.Unlock()
			return report(refreshed), nil
		}
		if coord.hash != hash && !compatiblePreparation {
			prepareCtx, cancel := context.WithCancel(ctx)
			coord.hash, coord.source, coord.done, coord.cancel, coord.result, coord.err = hash, append([]message.Message(nil), refreshed...), make(chan struct{}), cancel, nil, nil
			done := coord.done
			worker := c
			worker.compactHooks = nil // lifecycle hooks run only for a result that is activated.
			go func() {
				result, prepareErr := worker.prepareCompactionReason(prepareCtx, append([]message.Message(nil), refreshed...), hardTokens, "automatic_soft")
				coord.mu.Lock()
				if coord.hash == hash && coord.done == done {
					coord.result, coord.err, coord.cancel = result, prepareErr, nil
				}
				close(done)
				coord.mu.Unlock()
			}()
		}
		coord.mu.Unlock()
		return report(refreshed), nil
	}
	if coord.done != nil && compactionSourcePrefix(coord.source, refreshed) {
		done := coord.done
		coord.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return history, ctx.Err()
		}
		coord.mu.Lock()
		if coord.err == nil {
			if result, usable := preparedWithUncoveredTail(coord.result, coord.source, refreshed, c.compactTargetTokens); usable {
				_, manifest := extractContextCheckpoint(result)
				manifestHash := ""
				if manifest != nil {
					manifestHash = manifest.ManifestHash
				}
				activationIdentity := activeCacheIdentity(c.staticIdentity, manifestHash, compactionSummaryHash(result))
				if coord.activated != activationIdentity {
					if c.compactHooks != nil {
						if hookErr := c.compactHooks(ctx, refreshed, nil, nil); hookErr != nil {
							coord.mu.Unlock()
							return history, hookErr
						}
					}
					if c.activateCompaction != nil {
						if activateErr := c.activateCompaction(ctx, result, activationIdentity); activateErr != nil {
							if errors.Is(activateErr, session.ErrRunCheckpointStale) {
								coord.mu.Unlock()
								goto synchronous
							}
							coord.mu.Unlock()
							return history, activateErr
						}
					}
					coord.activated = activationIdentity
					if c.compactHooks != nil {
						_ = c.compactHooks(ctx, refreshed, result, nil)
					}
				}
				coord.mu.Unlock()
				return result, nil
			}
		}
		coord.mu.Unlock()
	} else {
		coord.mu.Unlock()
	}

synchronous:
	result, err := c.compactRequired(ctx, refreshed, hardTokens)
	if err == nil && !reflect.DeepEqual(result, refreshed) {
		result, err = c.activateCompactionResult(ctx, result)
	}
	return result, err
}

func (c turnContext) summarizeBounded(ctx context.Context, previous []string, omitted []message.Message) (string, error) {
	envelope, err := c.summarizeStateBounded(ctx, previous, omitted)
	return envelope.Body, err
}

func (c turnContext) summarizeStateBounded(ctx context.Context, previous []string, omitted []message.Message) (summaryEnvelope, error) {
	summarize := c.summarize
	budget := 0
	if c.resolveSummarizer != nil {
		var err error
		summarize, budget, err = c.resolveSummarizer(ctx)
		if err != nil {
			return summaryEnvelope{}, err
		}
	}
	if summarize == nil {
		return summaryEnvelope{}, fmt.Errorf("compaction model is unavailable")
	}
	if budget <= 0 {
		budget = 32000
	}
	maxBytes := contextTokenBytes(budget)
	var chunks [][]message.Message
	groups, err := compactionAtomicGroups(omitted)
	if err != nil {
		return summaryEnvelope{}, err
	}
	for _, group := range groups {
		atom := omitted[group.start:group.end]
		authorities := messageAuthorities(atom, c.runID)
		if len(serializeSemanticHistory(nil, atom, authorities)) > maxBytes {
			return summaryEnvelope{}, fmt.Errorf("compaction input: atomic group at message %d exceeds %d-token compactor budget", group.start, budget)
		}
		candidate := append([]message.Message(nil), atom...)
		if len(chunks) > 0 {
			candidate = append(append([]message.Message(nil), chunks[len(chunks)-1]...), atom...)
		}
		if len(chunks) == 0 || len(serializeSemanticHistory(nil, candidate, messageAuthorities(candidate, c.runID))) > maxBytes {
			chunks = append(chunks, append([]message.Message(nil), atom...))
		} else {
			chunks[len(chunks)-1] = append(chunks[len(chunks)-1], atom...)
		}
	}
	summaries := make([]summaryEnvelope, 0, len(previous)+len(chunks))
	for _, value := range previous {
		body := strings.TrimSpace(strings.TrimPrefix(value, semanticStateSafetyLabel))
		authorities := semanticStateAuthorities(body)
		if len(authorities) == 0 {
			return summaryEnvelope{}, fmt.Errorf("previous semantic state has no valid provenance")
		}
		normalized, err := normalizeSemanticStateV1(body, authorities)
		if err != nil {
			return summaryEnvelope{}, err
		}
		normalizedAuthorities := semanticStateAuthorities(normalized)
		summaries = append(summaries, summaryEnvelope{Body: normalized, Authorities: normalizedAuthorities, Digest: semanticSourceDigest(nil, normalizedAuthorities)})
	}
	for _, chunk := range chunks {
		authorities := messageAuthorities(chunk, c.runID)
		input := serializeSemanticHistory(nil, chunk, authorities)
		raw, err := summarize(ctx, input)
		if err != nil {
			return summaryEnvelope{}, err
		}
		normalized, normalizeErr := normalizeSemanticStateV1(raw, authorities)
		if normalizeErr != nil {
			repair := input + "\n\nThe prior output failed host validation: " + normalizeErr.Error() + ". Return one corrected SemanticStateV1 JSON object only."
			raw, err = summarize(ctx, repair)
			if err != nil {
				return summaryEnvelope{}, err
			}
			normalized, normalizeErr = normalizeSemanticStateV1(raw, authorities)
		}
		if normalizeErr != nil {
			return summaryEnvelope{}, normalizeErr
		}
		normalizedAuthorities := semanticStateAuthorities(normalized)
		summaries = append(summaries, summaryEnvelope{Body: normalized, Authorities: normalizedAuthorities, Digest: semanticSourceDigest(chunk, normalizedAuthorities)})
	}
	for len(summaries) > 1 {
		var next []summaryEnvelope
		for start := 0; start < len(summaries); {
			end := start + 1
			for end < len(summaries) && len(serializeSummaryEnvelopes(summaries[start:end+1])) <= maxBytes {
				end++
			}
			if end == start+1 && len(serializeSummaryEnvelopes(summaries[start:end])) > maxBytes {
				return summaryEnvelope{}, fmt.Errorf("compaction reduce input exceeds %d-token compactor budget", budget)
			}
			authorities := make(map[string]string)
			bodies := make([]string, 0, end-start)
			for _, item := range summaries[start:end] {
				bodies = append(bodies, item.Body)
				authorities = mergeAuthorities(authorities, item.Authorities)
			}
			input := serializeSemanticHistory(bodies, nil, authorities)
			raw, err := summarize(ctx, input)
			if err != nil {
				return summaryEnvelope{}, err
			}
			normalized, normalizeErr := normalizeSemanticStateV1(raw, authorities)
			if normalizeErr != nil {
				repair := input + "\n\nThe prior output failed host validation: " + normalizeErr.Error() + ". Return one corrected SemanticStateV1 JSON object only."
				raw, err = summarize(ctx, repair)
				if err != nil {
					return summaryEnvelope{}, err
				}
				normalized, normalizeErr = normalizeSemanticStateV1(raw, authorities)
			}
			if normalizeErr != nil {
				return summaryEnvelope{}, normalizeErr
			}
			normalizedAuthorities := semanticStateAuthorities(normalized)
			next = append(next, summaryEnvelope{Body: normalized, Authorities: normalizedAuthorities, Digest: semanticSourceDigest(nil, normalizedAuthorities)})
			start = end
		}
		if len(next) >= len(summaries) {
			return mergeSummaryEnvelopes(summaries)
		}
		summaries = next
	}
	if len(summaries) == 0 {
		return summaryEnvelope{}, fmt.Errorf("compaction produced no semantic state")
	}
	return summaries[0], nil
}

func serializeSummaryEnvelopes(envelopes []summaryEnvelope) string {
	bodies := make([]string, 0, len(envelopes))
	authorities := make(map[string]string)
	for _, envelope := range envelopes {
		bodies = append(bodies, envelope.Body)
		authorities = mergeAuthorities(authorities, envelope.Authorities)
	}
	return serializeSemanticHistory(bodies, nil, authorities)
}

func mergeSummaryEnvelopes(envelopes []summaryEnvelope) (summaryEnvelope, error) {
	if len(envelopes) == 0 {
		return summaryEnvelope{}, fmt.Errorf("compaction produced no semantic state")
	}
	var merged SemanticStateV1
	merged.Version = 1
	authorities := make(map[string]string)
	collections := map[string]map[string]StateFactV1{
		"acceptance": {}, "constraints": {}, "decisions": {}, "workset": {},
		"findings": {}, "failures": {}, "blockers": {}, "next": {},
	}
	for _, envelope := range envelopes {
		var current SemanticStateV1
		if err := json.Unmarshal([]byte(envelope.Body), &current); err != nil {
			return summaryEnvelope{}, err
		}
		merged.Objective = current.Objective
		if current.CurrentAction != nil {
			value := *current.CurrentAction
			merged.CurrentAction = &value
		}
		if current.ActiveTodoItemID != "" {
			merged.ActiveTodoItemID = current.ActiveTodoItemID
		}
		merged.RetrievalHints = boundedUniqueStrings(append(merged.RetrievalHints, current.RetrievalHints...), 32, 1024)
		for name, facts := range map[string][]StateFactV1{
			"acceptance": current.AcceptanceCriteria, "constraints": current.Constraints,
			"decisions": current.Decisions, "workset": current.Workset, "findings": current.Findings,
			"failures": current.Failures, "blockers": current.Blockers, "next": current.NextActions,
		} {
			for _, fact := range facts {
				collections[name][fact.ID] = fact
			}
		}
		authorities = mergeAuthorities(authorities, envelope.Authorities)
	}
	superseded := make(map[string]struct{})
	for _, facts := range collections {
		for _, fact := range facts {
			for _, id := range fact.Supersedes {
				superseded[id] = struct{}{}
			}
		}
	}
	ordered := func(name string) []StateFactV1 {
		ids := make([]string, 0, len(collections[name]))
		for id, fact := range collections[name] {
			if _, removed := superseded[id]; !removed && fact.Status != "superseded" && fact.Status != "invalidated" {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		result := make([]StateFactV1, 0, len(ids))
		for _, id := range ids {
			result = append(result, collections[name][id])
		}
		return result
	}
	merged.AcceptanceCriteria = ordered("acceptance")
	merged.Constraints = ordered("constraints")
	merged.Decisions = ordered("decisions")
	merged.Workset = ordered("workset")
	merged.Findings = ordered("findings")
	merged.Failures = ordered("failures")
	merged.Blockers = ordered("blockers")
	merged.NextActions = ordered("next")
	encoded, _ := json.Marshal(merged)
	normalized, err := normalizeSemanticStateV1(string(encoded), authorities)
	if err != nil {
		return summaryEnvelope{}, err
	}
	normalizedAuthorities := semanticStateAuthorities(normalized)
	return summaryEnvelope{Body: normalized, Authorities: normalizedAuthorities, Digest: semanticSourceDigest(nil, normalizedAuthorities)}, nil
}

type compactionAtomicGroup struct{ start, end int }

// compactionAtomicGroups keeps an assistant tool-call message and every
// immediately following result for its calls indivisible. Other messages are
// independently chunkable, including messages within the same user turn.
func compactionAtomicGroups(messages []message.Message) ([]compactionAtomicGroup, error) {
	if err := message.ValidateCompleteTurns(messages); err != nil {
		return nil, err
	}
	groups := make([]compactionAtomicGroup, 0, len(messages))
	for start := 0; start < len(messages); {
		end := start + 1
		calls := messages[start].ToolCalls
		if len(calls) > 0 {
			end += len(calls)
		}
		groups = append(groups, compactionAtomicGroup{start: start, end: end})
		start = end
	}
	return groups, nil
}

func (c turnContext) normalizeToolResults(ctx context.Context, history []message.Message) ([]message.Message, error) {
	threshold := c.largeToolTokens
	if threshold <= 0 {
		threshold = 12000
	}
	result := append([]message.Message(nil), history...)
	for index := range result {
		current := result[index].ToolResult
		if current == nil {
			continue
		}
		payload := []byte(current.Content)
		if current.Content == "" {
			payload = append([]byte(nil), current.Structured...)
		}
		originalTokens := (len(payload) + estimatedBytesPerToken - 1) / estimatedBytesPerToken
		if originalTokens <= threshold {
			continue
		}
		if c.putArtifact == nil {
			continue
		}
		artifact, err := c.putArtifact(ctx, "tool_result", payload, "")
		if err != nil {
			return nil, fmt.Errorf("externalize oversized tool result %q: %w", current.ToolCallID, err)
		}
		reference, _ := json.Marshal(map[string]any{
			"kind": "context_artifact", "tool": current.Name, "tool_call_id": current.ToolCallID,
			"sha256": artifact.SHA256, "artifact_ref": artifact.ID, "preview": artifact.Preview, "original_tokens": originalTokens,
		})
		cloned := *current
		cloned.Content = string(reference)
		cloned.Structured = nil
		result[index].ToolResult = &cloned
	}
	return result, nil
}

const estimatedBytesPerToken = 4

// estimateContextTokens follows the same bytes/4 heuristic as grok-build, but
// counts only fields that a provider can put on the wire. In particular, a
// tool result's Structured form is a fallback when Content is empty, not a
// second copy of the result sent to the model.
func estimateContextTokens(messages []message.Message) int {
	maxInt := int(^uint(0) >> 1)
	tokens, remainder := 0, 0
	addBytes := func(bytes int) {
		if bytes <= 0 || tokens == maxInt {
			return
		}
		whole, nextRemainder := bytes/estimatedBytesPerToken, bytes%estimatedBytesPerToken
		if whole > maxInt-tokens {
			tokens, remainder = maxInt, 0
			return
		}
		tokens += whole
		remainder += nextRemainder
		if remainder >= estimatedBytesPerToken {
			if tokens == maxInt {
				remainder = 0
				return
			}
			tokens++
			remainder -= estimatedBytesPerToken
		}
	}
	for _, current := range messages {
		addBytes(len(current.Text))
		addBytes(len(current.Thinking))
		addBytes(len(current.ThinkingSignature))
		addBytes(len(current.RedactedThinking))
		addBytes(len(current.ProviderState))
		for _, call := range current.ToolCalls {
			addBytes(len(call.ID))
			addBytes(len(call.Name))
			addBytes(len(call.Arguments))
		}
		if result := current.ToolResult; result != nil {
			addBytes(len(result.ToolCallID))
			addBytes(len(result.Name))
			if result.Content != "" {
				addBytes(len(result.Content))
			} else {
				addBytes(len(result.Structured))
			}
		}
	}
	if remainder > 0 && tokens < maxInt {
		tokens++
	}
	return tokens
}

func contextTokenBytes(tokens int) int {
	maxInt := int(^uint(0) >> 1)
	if tokens > maxInt/estimatedBytesPerToken {
		return maxInt
	}
	return tokens * estimatedBytesPerToken
}
