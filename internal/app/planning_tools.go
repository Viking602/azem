package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/tool"
)

const (
	askToolName        = "ask"
	submitPlanToolName = "submit_plan"
)

type askOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Recommended bool   `json:"recommended,omitempty"`
}

type askQuestion struct {
	ID            string      `json:"id"`
	Header        string      `json:"header"`
	Question      string      `json:"question"`
	Options       []askOption `json:"options"`
	AllowMultiple bool        `json:"allow_multiple,omitempty"`
}

type askInput struct {
	Questions []askQuestion `json:"questions"`
}

type askAnswer struct {
	QuestionID string   `json:"question_id"`
	Selected   []string `json:"selected,omitempty"`
	Other      string   `json:"other,omitempty"`
}

type askResponse struct {
	Answers []askAnswer `json:"answers"`
}

type liveUserInput struct {
	id        string
	sessionID string
	runID     string
	callID    string
	questions []askQuestion
	response  chan askResponse
}

type askDriver struct {
	sessionID string
	runID     string
	host      providerHost
}

func (d *askDriver) Definition() tool.Definition {
	additional := false
	option := tool.Schema{Type: "object", Properties: map[string]tool.Schema{
		"label": {Type: "string"}, "description": {Type: "string"}, "recommended": {Type: "boolean"},
	}, Required: []string{"label", "description"}, AdditionalProperties: &additional}
	question := tool.Schema{Type: "object", Properties: map[string]tool.Schema{
		"id": {Type: "string"}, "header": {Type: "string"}, "question": {Type: "string"},
		"options": {Type: "array", Items: &option}, "allow_multiple": {Type: "boolean"},
	}, Required: []string{"id", "header", "question", "options"}, AdditionalProperties: &additional}
	return tool.Definition{
		Name:        askToolName,
		Description: "Ask the user one to three decision questions that cannot be answered from the workspace. Call this tool by itself. The UI automatically allows a custom Other answer. Do not use it for approval or for repository facts.",
		InputSchema: tool.Schema{Type: "object", Properties: map[string]tool.Schema{
			"questions": {Type: "array", Items: &question},
		}, Required: []string{"questions"}, AdditionalProperties: &additional},
		EffectType: tool.EffectReadOnly, RequiresApproval: false, RequiresActionTask: false,
		RiskLevel: "low", Metadata: map[string]string{"approval": "allow", "interactive": "true", "exclusive": "true"},
		PolicyTags: []string{"session", "planning", "interactive"},
	}
}

func (d *askDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	if d.host == nil || d.host.Sessions() == nil {
		return planningToolError(call, "interactive planning is unavailable"), nil
	}
	questions, err := decodeAskQuestions(call.Arguments)
	if err != nil {
		return planningToolError(call, err.Error()), nil
	}
	requestID, err := planningRequestID(call.ID)
	if err != nil {
		return planningToolError(call, err.Error()), nil
	}
	encoded, _ := json.Marshal(questions)
	live := &liveUserInput{id: requestID, sessionID: d.sessionID, runID: d.runID, callID: call.ID, questions: questions, response: make(chan askResponse, 1)}
	if !d.host.RegisterUserInput(requestID, live) {
		return planningToolError(call, "interactive question ID collision"), nil
	}
	defer d.host.FinishUserInput(live)
	if _, err := d.host.Sessions().AppendBlock(ctx, d.sessionID, session.Block{
		Kind: "question", RunID: d.runID, Title: "User input", State: "pending",
		Data: map[string]string{"userInputId": requestID, "questions": string(encoded)},
	}); err != nil {
		return planningToolError(call, "persist question: "+err.Error()), nil
	}
	if !d.host.EmitEvent(ctx, Event{Kind: EventUserInputRequested, SessionID: d.sessionID, RunID: d.runID, ToolCallID: call.ID, UserInputID: requestID, State: "pending", Data: map[string]string{"questions": string(encoded)}}) {
		return planningToolError(call, eventDeliveryError(ctx).Error()), nil
	}
	select {
	case <-ctx.Done():
		_, _ = d.host.Sessions().UpdateLatestBlockState(context.WithoutCancel(ctx), d.sessionID, "question", "userInputId", requestID, "pending", "interrupted", nil)
		return planningToolError(call, ctx.Err().Error()), nil
	case response := <-live.response:
		payload, _ := json.Marshal(response)
		return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: formatAskResponse(response), Structured: payload}, nil
	}
}

func decodeAskQuestions(arguments json.RawMessage) ([]askQuestion, error) {
	var input askInput
	if err := json.Unmarshal(arguments, &input); err != nil {
		return nil, fmt.Errorf("decode questions: %w", err)
	}
	if err := validateAskQuestions(input.Questions); err != nil {
		return nil, err
	}
	return input.Questions, nil
}

func planningRequestID(callID string) (string, error) {
	if id := strings.TrimSpace(callID); id != "" {
		return id, nil
	}
	return randomID("ask")
}

func validateAskQuestions(questions []askQuestion) error {
	if len(questions) < 1 || len(questions) > 3 {
		return fmt.Errorf("ask requires one to three questions")
	}
	ids := map[string]struct{}{}
	for _, question := range questions {
		if err := validateAskQuestion(question, ids); err != nil {
			return err
		}
	}
	return nil
}

func validateAskQuestion(question askQuestion, ids map[string]struct{}) error {
	if strings.TrimSpace(question.ID) == "" || strings.TrimSpace(question.Header) == "" || strings.TrimSpace(question.Question) == "" {
		return fmt.Errorf("question id, header, and question are required")
	}
	if _, exists := ids[question.ID]; exists {
		return fmt.Errorf("duplicate question id %q", question.ID)
	}
	ids[question.ID] = struct{}{}
	if len(question.Options) < 2 || len(question.Options) > 5 {
		return fmt.Errorf("question %q requires two to five options", question.ID)
	}
	return validateAskOptions(question)
}

func validateAskOptions(question askQuestion) error {
	labels := map[string]struct{}{}
	for _, option := range question.Options {
		label := strings.TrimSpace(option.Label)
		if label == "" || strings.TrimSpace(option.Description) == "" {
			return fmt.Errorf("question %q option label and description are required", question.ID)
		}
		if _, exists := labels[label]; exists {
			return fmt.Errorf("question %q has duplicate option %q", question.ID, label)
		}
		labels[label] = struct{}{}
	}
	return nil
}

func validateAskResponse(questions []askQuestion, response askResponse) error {
	if len(response.Answers) != len(questions) {
		return fmt.Errorf("every question requires an answer")
	}
	answers, err := indexAskAnswers(response.Answers)
	if err != nil {
		return err
	}
	for _, question := range questions {
		if err := validateAskAnswer(question, answers[question.ID]); err != nil {
			return err
		}
	}
	return nil
}

func indexAskAnswers(values []askAnswer) (map[string]askAnswer, error) {
	answers := make(map[string]askAnswer, len(values))
	for _, answer := range values {
		if _, exists := answers[answer.QuestionID]; exists {
			return nil, fmt.Errorf("duplicate answer for %q", answer.QuestionID)
		}
		answers[answer.QuestionID] = answer
	}
	return answers, nil
}

func validateAskAnswer(question askQuestion, answer askAnswer) error {
	if answer.QuestionID == "" || (len(answer.Selected) == 0 && strings.TrimSpace(answer.Other) == "") {
		return fmt.Errorf("question %q requires an answer", question.ID)
	}
	if !question.AllowMultiple && len(answer.Selected) > 1 {
		return fmt.Errorf("question %q accepts only one option", question.ID)
	}
	allowed := map[string]struct{}{}
	for _, option := range question.Options {
		allowed[option.Label] = struct{}{}
	}
	for _, selected := range answer.Selected {
		if _, ok := allowed[selected]; !ok {
			return fmt.Errorf("question %q has unknown option %q", question.ID, selected)
		}
	}
	return nil
}

func formatAskResponse(response askResponse) string {
	lines := make([]string, 0, len(response.Answers))
	for _, answer := range response.Answers {
		values := append([]string(nil), answer.Selected...)
		if other := strings.TrimSpace(answer.Other); other != "" {
			values = append(values, other)
		}
		lines = append(lines, answer.QuestionID+": "+strings.Join(values, ", "))
	}
	return strings.Join(lines, "\n")
}

func planningToolError(call tool.Call, message string) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: message, IsError: true}
}

func (s *Service) finishUserInput(live *liveUserInput) {
	s.mu.Lock()
	if s.liveUserInputs[live.id] == live {
		delete(s.liveUserInputs, live.id)
	}
	s.mu.Unlock()
}

func (s *Service) resolveUserInput(ctx context.Context, sessionID, requestID string, payload json.RawMessage) error {
	var response askResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return fmt.Errorf("decode user input: %w", err)
	}
	s.mu.Lock()
	live := s.liveUserInputs[requestID]
	s.mu.Unlock()
	if live == nil {
		return s.resumeInterruptedPlanningInput(ctx, sessionID, requestID, response)
	}
	if strings.TrimSpace(sessionID) == "" || sessionID != live.sessionID {
		return fmt.Errorf("user input %q does not belong to session %q", requestID, sessionID)
	}
	if err := validateAskResponse(live.questions, response); err != nil {
		return err
	}
	encoded, _ := json.Marshal(response)
	if _, err := s.sessions.UpdateLatestBlockState(ctx, live.sessionID, "question", "userInputId", requestID, "pending", "answered", map[string]string{"answers": string(encoded)}); err != nil {
		return err
	}
	select {
	case live.response <- response:
	default:
		return fmt.Errorf("user input %q was already answered", requestID)
	}
	s.emit(ctx, Event{Kind: EventUserInputResolved, SessionID: live.sessionID, RunID: live.runID, ToolCallID: live.callID, UserInputID: requestID, State: "answered", Data: map[string]string{"answers": string(encoded)}})
	return nil
}

func (s *Service) resumeInterruptedPlanningInput(ctx context.Context, sessionID, requestID string, response askResponse) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("user input %q is not pending", requestID)
	}
	projection, err := s.sessions.LoadProjection(ctx, sessionID)
	if err != nil {
		return err
	}
	block, questions, err := pendingPlanningQuestion(projection.Blocks, requestID)
	if err != nil {
		return err
	}
	if err := validateAskResponse(questions, response); err != nil {
		return err
	}
	encoded, _ := json.Marshal(response)
	if _, err := s.sessions.UpdateLatestBlockState(ctx, sessionID, "question", "userInputId", requestID, block.State, "answered", map[string]string{"answers": string(encoded)}); err != nil {
		return err
	}
	current, err := s.sessions.LoadSession(ctx, sessionID)
	if err != nil {
		return err
	}
	runID, err := s.StartConfiguredTurn(TurnRequest{
		SessionID: sessionID,
		Prompt:    "The application restarted while waiting for planning input. The user answered:\n" + formatAskResponse(response) + "\n\nContinue the planning workflow and submit a complete revised plan for review.",
		Provider:  current.ProviderID, Model: current.ModelID, Reasoning: current.Reasoning, AgentMode: current.AgentMode,
		PlanMode: true,
	})
	if err != nil {
		_, _ = s.sessions.UpdateLatestBlockState(context.WithoutCancel(ctx), sessionID, "question", "userInputId", requestID, "answered", block.State, map[string]string{"resumeError": err.Error()})
		return err
	}
	s.emit(ctx, Event{Kind: EventUserInputResolved, SessionID: sessionID, RunID: runID, UserInputID: requestID, State: "answered", Data: map[string]string{"answers": string(encoded), "resumed": "true"}})
	return nil
}

func pendingPlanningQuestion(blocks []session.Block, requestID string) (*session.Block, []askQuestion, error) {
	for index := len(blocks) - 1; index >= 0; index-- {
		candidate := blocks[index]
		if !isPendingPlanningQuestion(candidate, requestID) {
			continue
		}
		var questions []askQuestion
		if err := json.Unmarshal([]byte(candidate.Data["questions"]), &questions); err != nil {
			return nil, nil, fmt.Errorf("decode persisted questions: %w", err)
		}
		return &candidate, questions, nil
	}
	return nil, nil, fmt.Errorf("user input %q is not pending", requestID)
}

func isPendingPlanningQuestion(block session.Block, requestID string) bool {
	return block.Kind == "question" && block.Data["userInputId"] == requestID && (block.State == "pending" || block.State == "interrupted")
}

type planArtifactV1 struct {
	Version int    `json:"version"`
	Title   string `json:"title"`
	Body    string `json:"body"`
}

type submitPlanInput struct {
	Title string `json:"title"`
	Plan  string `json:"plan"`
}

type submitPlanDriver struct {
	sessionID string
	runID     string
	host      providerHost
	planYolo  *config.ModelRouteConfig
}

func (d *submitPlanDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name: submitPlanToolName, Terminal: true,
		Description: "Submit one decision-complete implementation plan for user review. Explain why and how, and include a dependency-aware execution graph with exclusive ownership and evidence for non-trivial work. This ends the planning turn. The user may discuss, request revisions, or explicitly execute the latest version later.",
		InputSchema: tool.Schema{Type: "object", Properties: map[string]tool.Schema{
			"title": {Type: "string"}, "plan": {Type: "string"},
		}, Required: []string{"title", "plan"}, AdditionalProperties: &additional},
		EffectType: tool.EffectWrite, RequiresApproval: false, RequiresActionTask: false,
		RiskLevel: "low", Metadata: map[string]string{"approval": "allow", "terminal": "true"},
		PolicyTags: []string{"session", "planning"},
	}
}

func (d *submitPlanDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	if d.host == nil || d.host.Sessions() == nil {
		return planningToolError(call, "plan persistence is unavailable"), nil
	}
	input, err := decodeSubmittedPlan(call.Arguments)
	if err != nil {
		return planningToolError(call, err.Error()), nil
	}
	projection, err := d.host.Sessions().LoadProjection(ctx, d.sessionID)
	if err != nil {
		return planningToolError(call, err.Error()), nil
	}
	version := nextPlanVersion(projection.Blocks)
	_, _ = d.host.Sessions().UpdateLatestBlockState(ctx, d.sessionID, "plan", "", "", "proposed", "superseded", nil)
	payload, _ := json.Marshal(planArtifactV1{Version: version, Title: input.Title, Body: input.Plan})
	artifact, err := d.host.Sessions().PutArtifact(ctx, d.sessionID, d.runID, "plan_v1", payload, input.Title)
	if err != nil {
		return planningToolError(call, err.Error()), nil
	}
	state := "proposed"
	data := map[string]string{"planId": artifact.ID, "version": fmt.Sprint(version)}
	if d.planYolo != nil {
		state = "approved"
		data["planYolo"] = "true"
		data["targetProvider"], data["targetModel"], data["targetReasoning"] = d.planYolo.Provider, d.planYolo.Model, d.planYolo.Reasoning
	}
	if _, err := d.host.Sessions().AppendBlock(ctx, d.sessionID, session.Block{Kind: "plan", RunID: d.runID, Title: input.Title, Content: input.Plan, State: state, Data: data}); err != nil {
		return planningToolError(call, err.Error()), nil
	}
	if d.planYolo != nil {
		if err := d.host.RegisterPlanYoloHandoff(d.runID, d.sessionID, artifact.ID, input.Title, *d.planYolo); err != nil {
			return planningToolError(call, err.Error()), nil
		}
		if !d.host.EmitEvent(ctx, Event{Kind: EventPlanResolved, SessionID: d.sessionID, RunID: d.runID, ToolCallID: call.ID, PlanID: artifact.ID, Text: input.Plan, State: "handoff_pending", Data: map[string]string{"title": input.Title, "version": fmt.Sprint(version), "automatic": "true"}}) {
			return planningToolError(call, eventDeliveryError(ctx).Error()), nil
		}
		structured, _ := json.Marshal(map[string]any{"plan_id": artifact.ID, "version": version, "state": "approved", "automatic": true})
		return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "Plan approved automatically; implementation handoff starts when this planning turn closes.", Structured: structured}, nil
	}
	if !d.host.EmitEvent(ctx, Event{Kind: EventPlanProposed, SessionID: d.sessionID, RunID: d.runID, ToolCallID: call.ID, PlanID: artifact.ID, Text: input.Plan, State: "proposed", Data: map[string]string{"title": input.Title, "version": fmt.Sprint(version)}}) {
		return planningToolError(call, eventDeliveryError(ctx).Error()), nil
	}
	structured, _ := json.Marshal(map[string]any{"plan_id": artifact.ID, "version": version, "state": "proposed"})
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "Plan submitted for user review.", Structured: structured}, nil
}

func decodeSubmittedPlan(arguments json.RawMessage) (submitPlanInput, error) {
	var input submitPlanInput
	if err := json.Unmarshal(arguments, &input); err != nil {
		return input, fmt.Errorf("decode plan: %w", err)
	}
	input.Title, input.Plan = strings.TrimSpace(input.Title), strings.TrimSpace(input.Plan)
	if input.Title == "" || len([]rune(input.Title)) > 120 {
		return input, fmt.Errorf("plan title must contain 1 to 120 characters")
	}
	if input.Plan == "" {
		return input, fmt.Errorf("plan body is required")
	}
	return input, nil
}

func nextPlanVersion(blocks []session.Block) int {
	version := 1
	for _, block := range blocks {
		if block.Kind == "plan" {
			version++
		}
	}
	return version
}

func (s *Service) resolvePlan(ctx context.Context, sessionID, planID, decision string) error {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(planID) == "" {
		return fmt.Errorf("session and plan are required")
	}
	if decision != "execute" {
		return fmt.Errorf("invalid plan decision %q", decision)
	}
	artifact, err := s.sessions.LoadArtifact(ctx, sessionID, planID)
	if err != nil {
		return err
	}
	if artifact.Kind != "plan_v1" {
		return fmt.Errorf("artifact %q is not an executable plan", planID)
	}
	var plan planArtifactV1
	if err := json.Unmarshal(artifact.Payload, &plan); err != nil {
		return fmt.Errorf("decode approved plan: %w", err)
	}
	if _, err := s.sessions.UpdateLatestBlockState(ctx, sessionID, "plan", "planId", planID, "proposed", "approved", nil); err != nil {
		return err
	}
	current, err := s.sessions.LoadSession(ctx, sessionID)
	if err != nil {
		return err
	}
	runID, err := s.StartConfiguredTurn(TurnRequest{
		SessionID: sessionID, Prompt: "执行已批准的计划：" + plan.Title,
		Provider: current.ProviderID, Model: current.ModelID, Reasoning: current.Reasoning, AgentMode: current.AgentMode,
		PlanMode: false, DisableSubagents: false, approvedPlanArtifactID: planID,
	})
	if err != nil {
		_, _ = s.sessions.UpdateLatestBlockState(context.WithoutCancel(ctx), sessionID, "plan", "planId", planID, "approved", "proposed", map[string]string{"executionError": err.Error()})
		return err
	}
	s.emit(ctx, Event{Kind: EventPlanResolved, SessionID: sessionID, RunID: runID, PlanID: planID, State: "executing", Data: map[string]string{"title": plan.Title}})
	return nil
}

func (s *Service) approvedPlanContext(ctx context.Context, sessionID, planID string) (string, error) {
	artifact, err := s.sessions.LoadArtifact(ctx, sessionID, planID)
	if err != nil {
		return "", err
	}
	if artifact.Kind != "plan_v1" {
		return "", fmt.Errorf("artifact %q is not an approved plan", planID)
	}
	var plan planArtifactV1
	if err := json.Unmarshal(artifact.Payload, &plan); err != nil {
		return "", fmt.Errorf("decode approved plan: %w", err)
	}
	return fmt.Sprintf(`Plan ID: %s
Title: %s

%s

Execute this approved plan in a normal implementation turn. Preserve its scope and verification requirements. If current repository evidence makes a required step unsafe or impossible, stop and report the concrete conflict instead of silently changing the plan.

Approved-plan scheduling contract:
1. The parent agent owns orchestration, shared integration hotspots, conflict resolution, and final verification. Convert the plan's execution graph into the todo state before implementation and advance each item as soon as its evidence is complete.
2. Recompute the dependency-ready task frontier after every completion. When two or more independent bounded tasks are ready, prefer delegation and issue multiple subagent.spawn calls in one parallel tool batch instead of executing them serially in the parent context.
3. Select roles from the live subagent catalog. Give every subagent the exact Goal, Scope, Requirements, Constraints, Acceptance, and Expected evidence handoff required by the main instructions. Grant exclusive file or symbol ownership and state that other agents may be working concurrently.
4. Never let concurrent writers own the same file or shared integration boundary. Serialize those tasks or keep the hotspot parent-owned. Use foreground shared-workspace agents for read-only research, planning, review, and verification; use isolated background worktrees for write-capable delegation when the runtime offers them.
5. Track every delegated task by its returned task or run ID. Retrieve background output before consuming a dependency and distinguish running, completed, failed, cancelled, and stalled work. A terminal status without the requested artifacts or evidence is incomplete. Inspect the cause before retrying; retry only after changing the failed condition, otherwise reassign the task or complete it in the parent.
6. Inspect returned artifacts and actual repository state before integrating them. Prefer a review or verification subagent that did not author the change, but never treat its pass result as approval. The parent must independently inspect the relevant diff and files, reconcile all findings, and directly observe the required tests or checks before marking a task done.
7. Dispatch the next ready frontier only after dependencies and acceptance evidence are satisfied. Do not manufacture delegation overhead for a tiny linear task or when the live runtime has no suitable subagent. In that case execute directly, while preserving the same acceptance criteria.`, planID, plan.Title, plan.Body), nil
}
