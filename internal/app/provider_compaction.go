package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/api"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
)

type providerUsageBudget struct {
	mu        sync.Mutex
	maxTokens int64
	used      int64
}

func (b *providerUsageBudget) beforeRequest() error {
	if b == nil || b.maxTokens <= 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used >= b.maxTokens {
		return fmt.Errorf("%w: max tokens (%d/%d)", hyagent.ErrBudgetExhausted, b.used, b.maxTokens)
	}
	return nil
}

func (b *providerUsageBudget) add(usage hyprovider.Usage) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.used += int64(usage.TotalTokens)
	b.mu.Unlock()
}

type budgetedProviderDriver struct {
	inner  hyprovider.Driver
	budget *providerUsageBudget
}

type advisoryBudgetDriver struct {
	inner   hyprovider.Driver
	mu      sync.Mutex
	count   int
	limit   int
	enabled bool
	warned  bool
}

func (d *advisoryBudgetDriver) Metadata() hyprovider.Metadata { return d.inner.Metadata() }

func (d *advisoryBudgetDriver) Stream(ctx context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	if d.shouldInjectNotice() {
		request.Messages = withAdvisoryRequestNotice(request.Messages)
	}
	return d.inner.Stream(ctx, request)
}

func (d *advisoryBudgetDriver) shouldInjectNotice() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.count++
	showNotice := d.enabled && d.limit > 0 && d.count > d.limit && !d.warned
	if showNotice {
		d.warned = true
	}
	return showNotice
}

func withAdvisoryRequestNotice(messages []message.Message) []message.Message {
	notice := message.NewText(message.RoleSystem,
		"[Advisory request budget reached] Finish through the shortest correct path and summarize the result. This is not a cancellation or hard limit; continue when more work is required for correctness.")
	notice.Visibility = message.VisibilityPrivate
	insertAt := 0
	for insertAt < len(messages) && messages[insertAt].Role == message.RoleSystem {
		insertAt++
	}
	result := make([]message.Message, 0, len(messages)+1)
	result = append(result, messages[:insertAt]...)
	result = append(result, notice)
	return append(result, messages[insertAt:]...)
}

func (d *budgetedProviderDriver) Metadata() hyprovider.Metadata { return d.inner.Metadata() }

func (d *budgetedProviderDriver) Stream(ctx context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	if err := d.budget.beforeRequest(); err != nil {
		return nil, err
	}
	stream, err := d.inner.Stream(ctx, request)
	if err != nil {
		return nil, err
	}
	return &budgetedProviderStream{Stream: stream, budget: d.budget}, nil
}

type budgetedProviderStream struct {
	hyprovider.Stream
	budget *providerUsageBudget
}

func (s *budgetedProviderStream) Recv() (hyprovider.Event, error) {
	event, err := s.Stream.Recv()
	if err == nil && event.Kind == hyprovider.EventDone {
		s.budget.add(event.Usage)
	}
	return event, err
}

func configuredManualCompactionModel(models []config.LLMuxModelConfig, modelID string) (catalog.Model, bool) {
	for _, model := range configuredCatalogModels(models) {
		if model.MatchesID(modelID) && model.ContextWindow > 0 {
			return model, true
		}
	}
	return catalog.Model{}, false
}

func cachedCompactionCatalogModel(models []catalog.Model, modelID string) (catalog.Model, bool) {
	for _, model := range models {
		if model.MatchesID(modelID) && model.ContextWindow > 0 {
			return model, true
		}
	}
	return catalog.Model{}, false
}

func (r *ProviderRuntime) localCompactionAccountIDs(ctx context.Context, providerID string) ([]string, error) {
	accountIDs := []string{""}
	if r.auth == nil {
		return accountIDs, nil
	}
	accounts, err := r.auth.Accounts(ctx, providerID)
	if err != nil {
		return nil, fmt.Errorf("list local %s accounts: %w", providerID, err)
	}
	for _, account := range accounts {
		accountIDs = append(accountIDs, account.ID)
	}
	return accountIDs, nil
}

func (r *ProviderRuntime) cachedManualCompactionModel(ctx context.Context, providerID, modelID string) (catalog.Model, error) {
	if profile, ok := r.cfg.Providers.LLMux[providerID]; ok {
		if model, found := configuredManualCompactionModel(profile.Models, modelID); found {
			return model, nil
		}
	}
	if r.catalog == nil {
		return catalog.Model{}, fmt.Errorf("cached model metadata is unavailable for %s/%s", providerID, modelID)
	}
	accountIDs, err := r.localCompactionAccountIDs(ctx, providerID)
	if err != nil {
		return catalog.Model{}, err
	}
	seen := make(map[string]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		if _, duplicate := seen[accountID]; duplicate {
			continue
		}
		seen[accountID] = struct{}{}
		cached, found, err := r.catalog.Cached(ctx, providerID, accountID)
		if err != nil {
			return catalog.Model{}, fmt.Errorf("load cached %s model catalog: %w", providerID, err)
		}
		if found {
			if model, matched := cachedCompactionCatalogModel(cached.Models, modelID); matched {
				return model, nil
			}
		}
	}
	return catalog.Model{}, fmt.Errorf("cached model metadata is unavailable for %s/%s", providerID, modelID)
}

func manualCompactionMessages(ctx context.Context, manager turnContext, projection session.Projection) ([]message.Message, error) {
	source := manualCompactionSource(manager)
	if len(projection.ToolRecords) > 0 && !source.savedModelHistoryCompatible() {
		return nil, fmt.Errorf("manual compaction cannot preserve durable tool messages: current model history is unavailable")
	}
	messages, err := source.Build(ctx, api.Task{})
	if err != nil {
		return nil, err
	}
	messages, err = expandManualCompactionMessages(ctx, source, messages)
	if err != nil {
		return nil, err
	}
	return messages, validateManualCompactionToolMessages(messages, projection.ToolRecords)
}

func manualCompactionSource(manager turnContext) turnContext {
	manager.todo = session.TodoList{}
	manager.toolRecords = nil
	manager.loadTodo = nil
	return manager
}

func expandManualCompactionMessages(ctx context.Context, source turnContext, messages []message.Message) ([]message.Message, error) {
	prefixEnd := 0
	for prefixEnd < len(messages) && messages[prefixEnd].Role == message.RoleSystem {
		prefixEnd++
	}
	expanded, err := source.expandArchiveMessages(ctx, messages[prefixEnd:], make(map[string]struct{}))
	return append(append([]message.Message(nil), messages[:prefixEnd]...), expanded...), err
}

func validateManualCompactionToolMessages(messages []message.Message, records []session.ToolRecord) error {
	calls, results := manualCompactionToolNames(messages)
	for _, record := range records {
		if !manualToolRecordRequiresPair(record) {
			continue
		}
		if calls[record.ToolCallID] == record.Name && results[record.ToolCallID] == record.Name {
			continue
		}
		return fmt.Errorf("manual compaction cannot preserve durable tool message %q from run %q", record.ToolCallID, record.RunID)
	}
	return nil
}

func manualCompactionToolNames(messages []message.Message) (map[string]string, map[string]string) {
	calls := make(map[string]string)
	results := make(map[string]string)
	for _, current := range messages {
		indexManualToolCalls(calls, current.ToolCalls)
		indexManualToolResult(results, current.ToolResult)
	}
	return calls, results
}

func indexManualToolCalls(index map[string]string, calls []message.ToolCall) {
	for _, call := range calls {
		index[call.ID] = call.Name
	}
}

func indexManualToolResult(index map[string]string, result *message.ToolResult) {
	if result != nil {
		index[result.ToolCallID] = result.Name
	}
}

func manualToolRecordRequiresPair(record session.ToolRecord) bool {
	return record.State == session.ToolCompleted || record.State == session.ToolFailed
}

func (r *ProviderRuntime) PrepareManualCompaction(ctx context.Context, projection session.Projection) (session.ArchivePlan, bool, error) {
	if !manualCompactionEligible(projection.Blocks) {
		return session.ArchivePlan{}, false, nil
	}
	mainModel, err := r.cachedManualCompactionModel(ctx, projection.Session.ProviderID, projection.Session.ModelID)
	if err != nil {
		return session.ArchivePlan{}, false, err
	}
	manualBudget, err := calculateContextBudget(mainModel.ID, mainModel.ContextWindow, 0, r.cfg.Agents.Context)
	if err != nil {
		return session.ArchivePlan{}, false, err
	}
	todo := session.TodoList{}
	if r.host != nil && r.host.Sessions() != nil {
		todo, err = r.host.Sessions().LoadTodo(ctx, projection.Session.ID)
		if err != nil {
			return session.ArchivePlan{}, false, err
		}
	}
	manager := turnContext{
		sessionID: projection.Session.ID, runID: "manual-compaction", providerID: projection.Session.ProviderID, modelID: projection.Session.ModelID,
		instructions: mainInstructions, instructionFingerprint: mainInstructionFingerprint,
		history: projection.Blocks, modelHistory: projection.ModelHistory, checkpointBoundary: projection.ModelHistory.CoveredThroughSequence,
		staticIdentity: mainInstructionFingerprint, todo: todo, toolRecords: projection.ToolRecords,
		largeToolTokens: r.cfg.Agents.Context.LargeToolResultTokens, keepRecentTokens: manualBudget.KeepRecent,
	}
	configureArchiveStorage(&manager, r.host, projection.Session.ID, "manual-compaction")
	manager.archiveVisual = slices.Contains(mainModel.InputModalities, "image")
	messages, err := manualCompactionMessages(ctx, manager, projection)
	if err != nil {
		return session.ArchivePlan{}, false, fmt.Errorf("rebuild session context: %w", err)
	}
	compacted, err := manager.prepareArchiveCompaction(ctx, messages, manualBudget.Trigger, "manual")
	if err != nil {
		return session.ArchivePlan{}, false, fmt.Errorf("rebuild session context: %w", err)
	}
	manifest := extractArchiveContextManifestRecord(compacted)
	if manifest == nil {
		return session.ArchivePlan{}, false, fmt.Errorf("rebuild session context: checkpoint manifest is missing")
	}
	return session.ArchivePlan{
		ExpectedUpdatedAt: projection.UpdatedAt, ExpectedHighWater: canonicalProjectionHighWater(projection.Blocks),
		Manifest: manifest,
		ModelHistory: session.ModelHistory{
			ProviderID: projection.Session.ProviderID, ModelID: projection.Session.ModelID,
			InstructionFingerprint: mainInstructionFingerprint, StaticPrefixHash: mainInstructionFingerprint,
			WireVersion: session.CurrentWireVersion, Messages: compacted,
			SummaryHash: session.ModelCheckpointHash(compacted), ContextManifestHash: manifest.ManifestHash,
			PolicyVersion: manifest.PolicyVersion,
		},
	}, true, nil
}

func canonicalProjectionHighWater(blocks []session.Block) *int64 {
	var boundary *int64
	for _, block := range blocks {
		if block.Kind == "user" || block.Kind == "assistant" {
			value := block.Sequence
			boundary = &value
		}
	}
	return boundary
}

func manualCompactionEligible(blocks []session.Block) bool {
	users := 0
	for _, block := range blocks {
		if block.Kind == "user" && strings.TrimSpace(block.Content) != "" {
			users++
		}
	}
	return users > archiveRecentUserTurns
}
