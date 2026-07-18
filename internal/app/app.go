package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	authservice "github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/config"
	mcpruntime "github.com/Viking602/azem/internal/mcp"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/recovery"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/skills"
)

var ErrRunActive = errors.New("a run is already active")

type Service struct {
	cfg               config.Config
	events            chan Event
	ctx               context.Context
	cancel            context.CancelFunc
	mu                sync.Mutex
	activeRun         string
	activeSession     string
	activeEnd         context.CancelFunc
	wg                sync.WaitGroup
	shuttingDown      bool
	shutdownOnce      sync.Once
	shutdownDone      chan struct{}
	shutdownErr       error
	sessions          *session.Service
	coding            *agentservice.Service
	providers         *ProviderRuntime
	liveApprovals     map[string]*liveApproval
	teamApprovals     map[string]struct{}
	approvalMode      ApprovalMode
	autoReviewDenials map[string]*autoReviewDenialTracker
	mcp               *mcpruntime.Manager
	subagentStore     agentservice.SubagentRunStore
	authentication    *authservice.Service
	catalog           *catalog.Service
	recovery          recovery.Summary
	reconciler        ReconcileResolver
	skillCatalog      *skills.Catalog
}

func NewService(parent context.Context, cfg config.Config) *Service {
	ctx, cancel := context.WithCancel(parent)
	return &Service{
		cfg: cfg, events: make(chan Event, 256), ctx: ctx, cancel: cancel,
		shutdownDone: make(chan struct{}), liveApprovals: make(map[string]*liveApproval),
		teamApprovals: make(map[string]struct{}), autoReviewDenials: make(map[string]*autoReviewDenialTracker),
		approvalMode: ApprovalModePrompt,
	}
}

func (s *Service) AttachDurable(sessions *session.Service, coding *agentservice.Service) {
	s.sessions = sessions
	s.coding = coding
}

func (s *Service) AttachAuth(authentication *authservice.Service, modelCatalog *catalog.Service) {
	if s.authentication != nil && s.authentication != authentication {
		s.authentication.SetStatusChangeCallback(nil)
	}
	s.authentication = authentication
	s.catalog = modelCatalog
	if authentication != nil {
		authentication.SetStatusChangeCallback(s.handleAuthStatusChange)
	}
}

func (s *Service) handleAuthStatusChange(ctx context.Context, change authservice.AccountStatusChange) {
	data := map[string]string{"provider": change.Provider, "accountID": change.AccountID}
	if account, err := s.authentication.Account(ctx, change.Provider, change.AccountID); err == nil {
		data["email"] = account.Email
		data["displayName"] = account.DisplayName
		data["plan"] = account.Plan
	}
	s.emit(s.ctx, Event{Kind: EventAuthState, State: change.Status, Text: change.AccountID, Data: data})
	s.emitApprovalMode(s.ctx)
}

func (s *Service) emitApprovalMode(ctx context.Context) {
	available := false
	if s.authentication != nil {
		active, err := s.authentication.HasActiveChatGPTAccount(ctx)
		available = err == nil && active
	}
	s.mu.Lock()
	if !available && s.approvalMode == ApprovalModeAutoReview {
		s.approvalMode = ApprovalModePrompt
	}
	mode := s.approvalMode
	s.mu.Unlock()
	s.emit(ctx, Event{
		Kind: EventApprovalMode, State: string(mode),
		Data: map[string]string{"auto_review_available": strconv.FormatBool(available)},
	})
}

func (s *Service) AttachSkills(catalog *skills.Catalog) {
	s.skillCatalog = catalog
}

func (s *Service) AttachProviderRuntime(runtime *ProviderRuntime) {
	s.providers = runtime
	if runtime != nil {
		runtime.Attach(s, s.mcp, s.subagentStore)
	}
}

func (s *Service) AttachAgentExtensions(manager *mcpruntime.Manager, subagentStore agentservice.SubagentRunStore) {
	s.mcp = manager
	s.subagentStore = subagentStore
	if s.providers != nil {
		s.providers.Attach(s, manager, subagentStore)
	}
}

func (s *Service) AttachRecovery(summary recovery.Summary) {
	s.recovery = summary
}

func (s *Service) Authentication() *authservice.Service { return s.authentication }

func (s *Service) Catalog() *catalog.Service { return s.catalog }

func (s *Service) Bootstrap() {
	s.emit(s.ctx, Event{
		Kind: EventBootstrapDone, State: "ready", Text: s.cfg.Workspace.Root,
	})
	s.emitRecoveryState()
	s.emitApprovalMode(s.ctx)
}

func (s *Service) emitRecoveryState() {
	summary := s.recovery
	if summary.ExpiredLeases == 0 && summary.QuarantinedAttempts == 0 && summary.InterruptedSubagents == 0 && len(summary.Runs) == 0 && len(summary.Approvals) == 0 && len(summary.ReconcileAttempts) == 0 {
		return
	}
	type notice struct {
		Kind     string `json:"kind"`
		ID       string `json:"id"`
		RunID    string `json:"runId,omitempty"`
		TaskID   string `json:"taskId,omitempty"`
		Title    string `json:"title"`
		Detail   string `json:"detail,omitempty"`
		State    string `json:"state"`
		TokenID  string `json:"tokenId,omitempty"`
		ToolName string `json:"toolName,omitempty"`
	}
	notices := make([]notice, 0, len(summary.Approvals)+len(summary.ReconcileAttempts))
	for _, pending := range summary.Approvals {
		detail := firstNonempty(pending.Approval.RiskSummary, pending.Approval.Reason, pending.Approval.RequestedAction)
		notices = append(notices, notice{Kind: "approval", ID: pending.Approval.ApprovalID, RunID: pending.Approval.RunID, TaskID: pending.Approval.TaskID, Title: "Pending approval", Detail: detail, State: "pending", TokenID: pending.Token.TokenID})
	}
	for _, attempt := range summary.ReconcileAttempts {
		notices = append(notices, notice{Kind: "reconcile", ID: attempt.AttemptID, RunID: attempt.RunID, TaskID: attempt.TaskID, Title: "Unknown side effect", Detail: "Confirm the external outcome before continuing.", State: "unknown", ToolName: attempt.ToolName})
	}
	encoded, err := json.Marshal(notices)
	if err != nil {
		s.emit(s.ctx, Event{Kind: EventRunFailed, State: "recovery_projection_failed", Text: err.Error()})
		return
	}
	s.emit(s.ctx, Event{Kind: EventRecoveryState, State: "attention_required", Data: map[string]string{
		"items":                string(encoded),
		"runs":                 fmt.Sprint(len(summary.Runs)),
		"expiredLeases":        fmt.Sprint(summary.ExpiredLeases),
		"quarantinedAttempts":  fmt.Sprint(summary.QuarantinedAttempts),
		"interruptedSubagents": fmt.Sprint(summary.InterruptedSubagents),
	}})
}

func (s *Service) Agent() *agentservice.Service { return s.coding }

func (s *Service) NextEvent(ctx context.Context) (Event, error) {
	select {
	case <-ctx.Done():
		return Event{}, ctx.Err()
	case event, ok := <-s.events:
		if !ok {
			return Event{}, ioEOF{}
		}
		return event.Clone(), nil
	}
}

func (s *Service) StartTurn(prompt string) (string, error) {
	return s.StartConfiguredTurn(TurnRequest{Prompt: prompt})
}

func sessionProjectionData(projection session.Projection, blocks string) map[string]string {
	return map[string]string{
		"blocks": blocks, "lastRunID": projection.LastRunID,
		"provider": projection.Session.ProviderID, "model": projection.Session.ModelID,
		"reasoning": projection.Session.Reasoning, "agentMode": projection.Session.AgentMode,
	}
}

func (s *Service) materializeTurnSession(ctx context.Context, request TurnRequest) error {
	if s.sessions == nil {
		return nil
	}
	title := strings.TrimSpace(strings.SplitN(request.Prompt, "\n", 2)[0])
	if title == "" {
		title = "New session"
	}
	runes := []rune(title)
	if len(runes) > 80 {
		title = string(runes[:79]) + "…"
	}
	_, err := s.sessions.Ensure(ctx, session.Session{
		ID:         request.SessionID,
		Title:      title,
		ProviderID: request.Provider,
		ModelID:    request.Model,
		Reasoning:  request.Reasoning,
		AgentMode:  request.AgentMode,
	})
	return err
}

func (s *Service) StartConfiguredTurn(request TurnRequest) (string, error) {
	request = normalizeTurnRequest(request, s.cfg.Defaults)
	if request.Prompt == "" {
		return "", fmt.Errorf("prompt is empty")
	}
	if request.AgentMode == "team" && len(request.ActiveSkills) > 0 {
		return "", fmt.Errorf("active skills require single-agent mode")
	}
	if len(request.ActiveSkills) > 0 {
		snapshot := s.coding.SkillSnapshot()
		if _, err := snapshot.Registry.Resolve(request.ActiveSkills...); err != nil {
			return "", err
		}
	}
	s.mu.Lock()
	if s.shuttingDown {
		s.mu.Unlock()
		return "", fmt.Errorf("application is shutting down")
	}
	if s.activeRun != "" {
		s.mu.Unlock()
		return "", ErrRunActive
	}
	runCtx, cancel := context.WithCancel(s.ctx)
	s.activeRun = "starting"
	s.activeSession = request.SessionID
	s.activeEnd = cancel
	s.mu.Unlock()
	if err := s.materializeTurnSession(s.ctx, request); err != nil {
		cancel()
		s.clearRun("starting")
		return "", fmt.Errorf("create session: %w", err)
	}
	if s.sessions != nil {
		projection, err := s.sessions.LoadProjection(s.ctx, request.SessionID)
		if err != nil {
			cancel()
			s.clearRun("starting")
			return "", err
		}
		request.History = append([]session.Block(nil), projection.Blocks...)
	}

	if s.providers == nil {
		runID, err := randomID("run")
		if err != nil {
			cancel()
			s.clearRun("starting")
			return "", err
		}
		if err := s.persistSessionPreferences(s.ctx, request); err != nil {
			cancel()
			s.clearRun("starting")
			return "", err
		}
		s.mu.Lock()
		s.activeRun = runID
		s.mu.Unlock()
		if s.sessions != nil {
			if err := s.sessions.AppendBlock(s.ctx, request.SessionID, session.Block{Kind: "user", RunID: runID, Title: "You", Content: request.Prompt}); err != nil {
				cancel()
				s.clearRun(runID)
				return "", fmt.Errorf("persist user turn: %w", err)
			}
		}
		s.wg.Add(1)
		go s.runFakeTurn(runCtx, request.SessionID, runID, request.Prompt)
		return runID, nil
	}

	if request.AgentMode == "team" {
		goal := teamPrompt(request)
		modelID, resolver, err := s.providers.TeamResolver(runCtx, request)
		if err != nil {
			cancel()
			s.clearRun("starting")
			return "", err
		}
		request.Model = modelID
		if err := s.persistSessionPreferences(s.ctx, request); err != nil {
			cancel()
			s.clearRun("starting")
			return "", err
		}
		runID, err := randomID("team")
		if err != nil {
			cancel()
			s.clearRun("starting")
			return "", err
		}
		s.mu.Lock()
		s.activeRun = runID
		s.mu.Unlock()
		if s.sessions != nil {
			if err := s.sessions.AppendBlock(s.ctx, request.SessionID, session.Block{Kind: "user", RunID: runID, Title: "You", Content: request.Prompt}); err != nil {
				cancel()
				s.clearRun(runID)
				return "", fmt.Errorf("persist user turn: %w", err)
			}
		}
		s.wg.Add(1)
		go s.runProviderTeam(runCtx, request, runID, goal, modelID, resolver)
		return runID, nil
	}

	durableRun, engine, err := s.providers.Start(runCtx, request)
	if err != nil {
		cancel()
		s.clearRun("starting")
		return "", err
	}
	if err := s.persistSessionPreferences(s.ctx, request); err != nil {
		cancel()
		_ = s.coding.CompleteRun(context.WithoutCancel(s.ctx), durableRun, err.Error(), err)
		s.clearRun("starting")
		return "", err
	}
	engine = s.bindProviderEngine(engine)
	s.mu.Lock()
	s.activeRun = durableRun.RunID
	s.mu.Unlock()
	if s.sessions != nil {
		if err := s.sessions.AppendBlock(s.ctx, request.SessionID, session.Block{Kind: "user", RunID: durableRun.RunID, Title: "You", Content: request.Prompt}); err != nil {
			cancel()
			_ = s.coding.CompleteRun(context.WithoutCancel(s.ctx), durableRun, err.Error(), err)
			s.clearRun(durableRun.RunID)
			return "", fmt.Errorf("persist user turn: %w", err)
		}
	}
	s.wg.Add(1)
	go s.runProviderTurn(runCtx, request, durableRun, engine)
	return durableRun.RunID, nil
}

func (s *Service) persistSessionPreferences(ctx context.Context, request TurnRequest) error {
	if s.sessions == nil {
		return nil
	}
	return s.sessions.UpdatePreferences(ctx, request.SessionID, request.Provider, request.Model, request.Reasoning, request.AgentMode)
}

func (s *Service) CancelActive() bool {
	return s.CancelActiveWithChildren(false)
}

func (s *Service) HasActiveForegroundChildren() bool {
	s.mu.Lock()
	sessionID, runID, providers := s.activeSession, s.activeRun, s.providers
	s.mu.Unlock()
	return providers != nil && sessionID != "" && runID != "" && runID != "starting" &&
		providers.HasActiveForegroundSubagents(sessionID, runID)
}

func (s *Service) CancelActiveWithChildren(children bool) bool {
	s.mu.Lock()
	cancel := s.activeEnd
	sessionID, runID, providers := s.activeSession, s.activeRun, s.providers
	s.mu.Unlock()
	if cancel == nil {
		return false
	}
	if children && providers != nil && sessionID != "" && runID != "" && runID != "starting" {
		providers.CancelParentSubagents(sessionID, runID)
	}
	cancel()
	return true
}

func (s *Service) Shutdown(ctx context.Context) error {
	s.shutdownOnce.Do(func() {
		go s.shutdown()
	})
	select {
	case <-s.shutdownDone:
		return s.shutdownErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) shutdown() {
	defer close(s.shutdownDone)
	s.mu.Lock()
	s.shuttingDown = true
	if s.activeEnd != nil {
		s.activeEnd()
	}
	s.mu.Unlock()
	subagentShutdownCtx, subagentShutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if s.providers != nil {
		if err := s.providers.Shutdown(subagentShutdownCtx); err != nil {
			s.shutdownErr = errors.Join(s.shutdownErr, err)
		}
	}
	subagentShutdownCancel()
	s.cancel()
	s.wg.Wait()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s.mcp != nil {
		if err := s.mcp.Close(); err != nil {
			s.shutdownErr = errors.Join(s.shutdownErr, err)
		}
	}
	if s.coding != nil {
		if err := s.coding.Checkpoint(shutdownCtx); err != nil {
			s.shutdownErr = errors.Join(s.shutdownErr, err)
		}
	}
	close(s.events)
	if s.coding != nil {
		if err := s.coding.Close(shutdownCtx); err != nil {
			s.shutdownErr = errors.Join(s.shutdownErr, err)
		}
	}
}

func (s *Service) runFakeTurn(ctx context.Context, sessionID string, runID string, prompt string) {
	defer s.wg.Done()
	defer s.clearRun(runID)
	s.emit(ctx, Event{Kind: EventRunStarted, SessionID: sessionID, RunID: runID, State: "running"})
	response := "Deterministic probe response: " + prompt
	var streamed strings.Builder
	parts := strings.Fields(response)
	for index, part := range parts {
		text := part
		if index < len(parts)-1 {
			text += " "
		}
		timer := time.NewTimer(35 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			if s.sessions != nil && streamed.Len() > 0 {
				_ = s.sessions.AppendBlock(s.ctx, sessionID, session.Block{Kind: "assistant", RunID: runID, Title: "Azem", Content: streamed.String(), State: "cancelled"})
			}
			s.emit(s.ctx, Event{Kind: EventRunCancelled, SessionID: sessionID, RunID: runID, State: "cancelled"})
			return
		case <-timer.C:
		}
		if !s.emit(ctx, Event{Kind: EventTextDelta, SessionID: sessionID, RunID: runID, Text: text, State: "streaming"}) {
			s.emit(s.ctx, Event{Kind: EventRunCancelled, SessionID: sessionID, RunID: runID, State: "cancelled"})
			return
		}
		streamed.WriteString(text)
	}
	if s.sessions != nil {
		if err := s.sessions.AppendBlock(ctx, sessionID, session.Block{Kind: "assistant", RunID: runID, Title: "Azem", Content: streamed.String(), State: "completed"}); err != nil {
			s.emit(ctx, Event{Kind: EventRunFailed, SessionID: sessionID, RunID: runID, State: "failed", Text: err.Error()})
			return
		}
	}
	s.emit(ctx, Event{Kind: EventRunFinished, SessionID: sessionID, RunID: runID, State: "completed"})
}

func (s *Service) canStartAutoWake(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shuttingDown || s.activeRun != "" {
		return false
	}
	for _, approval := range s.liveApprovals {
		if approval.sessionID == sessionID && !approval.resolved {
			return false
		}
	}
	return true
}

func (s *Service) startSubagentAutoWake(run agentservice.SubagentRun) error {
	if s.sessions == nil {
		return fmt.Errorf("session service is unavailable")
	}
	saved, err := s.sessions.LoadSession(s.ctx, run.SessionID)
	if err != nil {
		return err
	}
	result := firstNonempty(run.Output, run.Error, run.Summary)
	runes := []rune(result)
	if len(runes) > 4000 {
		result = string(runes[:4000]) + "\n[truncated]"
	}
	prompt := fmt.Sprintf(
		"Background subagent %s (%s) reached %s.\nResult:\n%s\n\nIncorporate this result into the prior request. Do not spawn or call subagents for this wake-up.",
		run.ID, run.Type, run.State, result,
	)
	_, err = s.StartConfiguredTurn(TurnRequest{
		SessionID: saved.ID, Prompt: prompt, Provider: saved.ProviderID, Model: saved.ModelID,
		Reasoning: saved.Reasoning, AgentMode: "single", DisableSubagents: true,
	})
	return err
}

func (s *Service) clearRun(runID string) {
	s.mu.Lock()
	sessionID := ""
	providers := s.providers
	delete(s.autoReviewDenials, runID)
	if s.activeRun == runID {
		sessionID = s.activeSession
		s.activeRun = ""
		s.activeSession = ""
		s.activeEnd = nil
	}
	s.mu.Unlock()
	if sessionID != "" && providers != nil {
		providers.AutoWakePending(sessionID)
	}
}

func (s *Service) emit(ctx context.Context, event Event) bool {
	event.At = time.Now().UTC()
	select {
	case <-ctx.Done():
		return false
	case s.events <- event.Clone():
		return true
	}
}

func randomID(prefix string) (string, error) {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "_" + hex.EncodeToString(bytes[:]), nil
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type ioEOF struct{}

func (ioEOF) Error() string { return "application event stream closed" }
