package app

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Viking602/azem/internal/contextarchive"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/api"
	"github.com/Viking602/venat/message"
)

//go:embed prompts/main.md
var mainInstructions string

//go:embed prompts/plan.md
var planModeInstructions string

const (
	failedAssistantLabel   = "[Incomplete assistant output from a failed attempt; treat it as uncommitted work.]\n"
	archiveRecentUserTurns = 3
)

var mainInstructionFingerprint = func() string {
	sum := sha256.Sum256([]byte(mainInstructions))
	return hex.EncodeToString(sum[:])
}()

func turnInstructions(planMode bool) (string, string) {
	instructions := mainInstructions
	if planMode {
		instructions = planModeInstructions
	}
	sum := sha256.Sum256([]byte(instructions))
	return instructions, hex.EncodeToString(sum[:])
}

// InstructionFingerprint returns the stable identity of the executable prompt
// selected for a turn without exposing or duplicating its contents.
func InstructionFingerprint(planMode bool) string {
	_, fingerprint := turnInstructions(planMode)
	return fingerprint
}

type TurnRequest struct {
	SessionID              string
	Prompt                 string
	Provider               string
	Model                  string
	History                []session.Block
	Reasoning              string
	AgentMode              string
	PlanMode               bool
	DisableSubagents       bool
	ActiveSkills           []string
	Images                 []session.Attachment
	Todo                   session.TodoList
	privateContext         string
	visionContext          string
	approvedPlanArtifactID string
	approvedPlanContext    string
	accountID              string
	historicalContext      string
	resuming               bool
	budgetRestored         bool
	maxTokens              int64
	maxToolCalls           int
	maxWallClock           time.Duration
	startedAt              time.Time
	usedTokens             int64
	usedToolCalls          int
	modelHistory           session.ModelHistory
	toolRecords            []session.ToolRecord
	checkpointBoundary     *int64
	immutableIdentity      string
	origin                 string
	wakeData               map[string]string
}

const (
	turnOriginSubagentWake = "subagent_wake"
	subagentWakeBlockState = "subagent_wake"
)

type providerContextPressure struct {
	toolTokens            int
	reportedHistoryTokens atomic.Int64
}

func (p *providerContextPressure) observeInputTokens(inputTokens int) {
	if p == nil || inputTokens <= 0 {
		return
	}
	p.reportedHistoryTokens.Store(int64(max(0, inputTokens-max(0, p.toolTokens))))
}

func (p *providerContextPressure) tokens(localEstimate int) int {
	if p == nil {
		return localEstimate
	}
	return max(localEstimate, int(p.reportedHistoryTokens.Load()))
}

func (p *providerContextPressure) reset() {
	if p != nil {
		p.reportedHistoryTokens.Store(0)
	}
}

type turnContext struct {
	sessionID                 string
	instructions              string
	instructionFingerprint    string
	providerID                string
	modelID                   string
	runID                     string
	privateContext            string
	visionContext             string
	approvedPlanContext       string
	historicalContext         string
	deadlineAt                time.Time
	resuming                  bool
	history                   []session.Block
	modelHistory              session.ModelHistory
	toolRecords               []session.ToolRecord
	workspaceRoot             string
	images                    []session.Attachment
	checkpointBoundary        *int64
	canonicalHighWater        *int64
	providerPressure          *providerContextPressure
	reportContextTokens       func(context.Context, int)
	compactHooks              func(context.Context, []message.Message, []message.Message, error) error
	putArtifact               func(context.Context, string, []byte, string) (session.ContextArtifact, error)
	archiveEnabled            bool
	archiveVisual             bool
	storeArchive              func(context.Context, contextarchive.Result) (contextarchive.Manifest, []session.Attachment, error)
	loadArchiveSource         func(context.Context, string) ([]byte, error)
	readArchiveAttachment     func(context.Context, session.Attachment) ([]byte, error)
	largeToolTokens           int
	keepRecentTokens          int
	todo                      session.TodoList
	loadTodo                  func(context.Context) (session.TodoList, error)
	staticIdentity            string
	coordinator               *compactionCoordinator
	activateCompaction        func(context.Context, []message.Message, string) error
	reportCachePrefixDegraded func(reason string)
}

// compactionCoordinator serializes durable activation for one live run.
type compactionCoordinator struct {
	mu        sync.Mutex
	activated string
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
	manifest := extractArchiveContextManifest(result)
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

func (c turnContext) savedModelHistoryCompatible() bool {
	saved := c.modelHistory
	fingerprint := c.instructionFingerprint
	if fingerprint == "" {
		fingerprint = mainInstructionFingerprint
	}
	staticPrefixCompatible := saved.StaticPrefixHash == fingerprint ||
		(c.staticIdentity != "" && saved.StaticPrefixHash == c.staticIdentity)
	return len(saved.Messages) > 0 &&
		saved.ProviderID == c.providerID &&
		saved.ModelID == c.modelID &&
		saved.InstructionFingerprint == fingerprint &&
		staticPrefixCompatible &&
		saved.WireVersion == session.CurrentWireVersion &&
		saved.CoveredThroughSequence != nil && c.checkpointBoundary != nil &&
		*saved.CoveredThroughSequence == *c.checkpointBoundary
}

func (c turnContext) Build(ctx context.Context, task api.Task) ([]message.Message, error) {
	saved := c.modelHistory
	compatible := c.savedModelHistoryCompatible()
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
	if text := runtimeDeadlineContext(ctx, c.deadlineAt); text != "" {
		value := message.NewText(message.RoleSystem, "[Trusted runtime deadline]\n"+text)
		value.Visibility = message.VisibilityPrivate
		messages = append(messages, value)
	}
	if text := strings.TrimSpace(c.approvedPlanContext); text != "" {
		value := message.NewText(message.RoleSystem, "[Trusted approved execution plan]\n"+text)
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
	if visual := visionEvidenceText(c.visionContext); visual != "" {
		data := message.NewText(message.RoleUser, visual)
		data.Visibility = message.VisibilityPrivate
		messages = append(messages, data)
	}
	goal := strings.TrimSpace(task.Goal)
	images := c.images
	for _, block := range c.history {
		if c.runID != "" && block.RunID == c.runID && block.Kind == "user" {
			goal = ""
			images = nil
			break
		}
	}
	if goal != "" || len(images) > 0 {
		messages = append(messages, UserMessageWithAttachments(goal, images))
	}
	messages, err = c.repairArchiveMessages(ctx, messages)
	if err != nil {
		return nil, err
	}
	if err := c.validateModelVisibleDurability(messages, compatible, goal, images); err != nil {
		return nil, err
	}
	return messages, nil
}

func runtimeDeadlineContext(ctx context.Context, configured time.Time) string {
	deadline := configured
	if ctx != nil {
		if ctxDeadline, ok := ctx.Deadline(); ok && (deadline.IsZero() || ctxDeadline.Before(deadline)) {
			deadline = ctxDeadline
		}
	}
	if deadline.IsZero() {
		return ""
	}
	remaining := time.Until(deadline)
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("Hard stop in %s (at %s UTC). Deliver a verifiable subset before the stop. Do not start work that cannot finish.", remaining.Round(time.Second), deadline.UTC().Format("2006-01-02T15:04:05Z"))
}

// validateModelVisibleDurability enforces the model-visible ⟺ durably-logged
// invariant on the assembled turn context. Every message the provider will
// see as regular conversation must be reconstructible from durable state: the
// persisted ModelHistory checkpoint, durable transcript blocks, the static
// instruction prompt, or the current goal (persisted as a user block by the
// caller). Private messages are exempt because each private source (semantic
// checkpoint, todo, plan artifact, tool continuity, hook/vision/historical
// evidence) is derived from a durable store or deterministic re-execution by
// construction. A violation fails the turn explicitly instead of silently
// sending unlogged context to the provider.
func (c turnContext) validateModelVisibleDurability(messages []message.Message, compatible bool, goal string, images []session.Attachment) error {
	durable := make(map[string]struct{}, len(c.modelHistory.Messages)+len(c.history)+2)
	admit := func(value message.Message) {
		durable[durableMessageKey(value)] = struct{}{}
	}
	if compatible {
		for _, value := range c.modelHistory.Messages {
			admit(value)
		}
	}
	for _, block := range c.history {
		if value, ok := blockMessage(block); ok {
			admit(value)
		}
	}
	if c.instructions != "" {
		admit(message.NewText(message.RoleSystem, c.instructions))
	}
	if goal != "" || len(images) > 0 {
		admit(UserMessageWithAttachments(goal, images))
	}
	for _, value := range messages {
		if value.Visibility == message.VisibilityPrivate {
			continue
		}
		if _, ok := durable[durableMessageKey(value)]; !ok {
			return fmt.Errorf(
				"turn context: model-visible %s message (kind %q) is not reconstructible from durable state; persist it before it enters the provider request",
				value.Role, value.Kind,
			)
		}
	}
	return nil
}

func durableMessageKey(value message.Message) string {
	value.CreatedAt = time.Time{}
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("unencodable:%s:%s:%s", value.Role, value.Kind, value.Text)
	}
	return string(payload)
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

func (c turnContext) Compact(ctx context.Context, history []message.Message) ([]message.Message, error) {
	target := max(512, estimateContextTokens(history)*3/4)
	return c.CompactTo(ctx, history, target)
}

// CompactTo deterministically archives complete old turns toward an absolute
// token target. The target is also the hard bound used to validate the
// mandatory latest-three-turn suffix.

func (c turnContext) CompactTo(ctx context.Context, history []message.Message, targetTokens int) ([]message.Message, error) {
	return c.archiveCompactTo(ctx, history, targetTokens)
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

func recentUserIndexes(history []message.Message, prefixEnd, count int) []int {
	indexes := make([]int, 0, count)
	for index := len(history) - 1; index >= prefixEnd && len(indexes) < count; index-- {
		if history[index].Role == message.RoleUser && history[index].Visibility != message.VisibilityPrivate {
			indexes = append(indexes, index)
		}
	}
	for left, right := 0, len(indexes)-1; left < right; left, right = left+1, right-1 {
		indexes[left], indexes[right] = indexes[right], indexes[left]
	}
	return indexes
}

// pruneToolResultMinBytes is the floor below which pruning an old tool result
// is not worth an artifact row. Context-artifact locators produced by earlier
// normalization or pruning stay well under this floor, so a result is never
// re-externalized.
const pruneToolResultMinBytes = 1 << 10

// pruneStaleToolResults is the model-free pruning layer that runs before
// archival compaction. It rewrites large tool-result bodies that precede the
// preserved recent user turns into durable context-artifact locators, oldest
// first, stopping as soon as the history fits the target. Only result content
// is replaced in place, so tool call/result pairing and message order are
// preserved and ValidateCompleteTurns semantics cannot change.
func (c turnContext) pruneStaleToolResults(ctx context.Context, history []message.Message, targetTokens int) ([]message.Message, bool, error) {
	if targetTokens <= 0 || c.putArtifact == nil || estimateContextTokens(history) <= targetTokens {
		return history, false, nil
	}
	prefixEnd := 0
	for prefixEnd < len(history) && history[prefixEnd].Role == message.RoleSystem {
		prefixEnd++
	}
	recentUsers := recentUserIndexes(history, prefixEnd, archiveRecentUserTurns)
	if len(recentUsers) == 0 {
		return history, false, nil
	}
	boundary := recentUsers[0]
	result := append([]message.Message(nil), history...)
	changed := false
	for index := prefixEnd; index < boundary; index++ {
		current := result[index].ToolResult
		if current == nil {
			continue
		}
		payload := []byte(current.Content)
		if current.Content == "" {
			payload = append([]byte(nil), current.Structured...)
		}
		if len(payload) <= pruneToolResultMinBytes {
			continue
		}
		artifact, err := c.putArtifact(ctx, "tool_result", payload, "")
		if err != nil {
			return history, false, fmt.Errorf("prune stale tool result %q: %w", current.ToolCallID, err)
		}
		reference, _ := json.Marshal(map[string]any{
			"kind": "context_artifact", "tool": current.Name, "tool_call_id": current.ToolCallID,
			"sha256": artifact.SHA256, "artifact_ref": artifact.ID, "preview": artifact.Preview,
			"original_tokens": (len(payload) + estimatedBytesPerToken - 1) / estimatedBytesPerToken,
			"pruned":          true,
		})
		cloned := *current
		cloned.Content = string(reference)
		cloned.Structured = nil
		result[index].ToolResult = &cloned
		changed = true
		if estimateContextTokens(result) <= targetTokens {
			break
		}
	}
	if !changed {
		return history, false, nil
	}
	return result, true, nil
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
		if archive, ok := archiveManifestFromMessage(current); ok && archive.FrameCount > 0 {
			if archive.FrameCount > maxInt/archiveImageTokenEstimate {
				tokens, remainder = maxInt, 0
				continue
			}
			frameTokens := archive.FrameCount * archiveImageTokenEstimate
			if frameTokens > maxInt-tokens {
				tokens, remainder = maxInt, 0
			} else {
				tokens += frameTokens
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
