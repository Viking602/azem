package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"
)

const (
	checkpointToolName     = "checkpoint"
	rewindToolName         = "rewind"
	checkpointArtifactKind = session.InternalArtifactKindPrefix + "context-checkpoint-v1"
)

type persistedCheckpointState struct {
	Version   int               `json:"version"`
	Active    bool              `json:"active"`
	Goal      string            `json:"goal,omitempty"`
	StartedAt time.Time         `json:"startedAt,omitempty"`
	Snapshot  []message.Message `json:"snapshot,omitempty"`
	Report    string            `json:"report,omitempty"`
	RewoundAt time.Time         `json:"rewoundAt,omitempty"`
	SavedAt   time.Time         `json:"savedAt"`
}

type checkpointController struct {
	mu            sync.Mutex
	store         *session.Service
	sessionID     string
	runID         string
	state         persistedCheckpointState
	pendingReport string
}

type checkpointDriver struct {
	operation  string
	controller *checkpointController
}

func newCheckpointController(ctx context.Context, store *session.Service, sessionID, runID string) (*checkpointController, error) {
	controller := &checkpointController{store: store, sessionID: sessionID, runID: runID, state: persistedCheckpointState{Version: 1}}
	if store == nil {
		return controller, nil
	}
	artifact, err := store.LoadLatestArtifactByKind(ctx, sessionID, checkpointArtifactKind)
	if errors.Is(err, session.ErrContextArtifactNotFound) {
		return controller, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load context checkpoint: %w", err)
	}
	if err := json.Unmarshal(artifact.Payload, &controller.state); err != nil {
		return nil, fmt.Errorf("decode context checkpoint: %w", err)
	}
	if controller.state.Version != 1 {
		return nil, fmt.Errorf("context checkpoint version %d is unsupported", controller.state.Version)
	}
	controller.state.Snapshot = cloneCheckpointMessages(controller.state.Snapshot)
	return controller, nil
}

func (controller *checkpointController) drivers() []tool.Driver {
	return []tool.Driver{
		&checkpointDriver{operation: checkpointToolName, controller: controller},
		&checkpointDriver{operation: rewindToolName, controller: controller},
	}
}

func (driver *checkpointDriver) Definition() tool.Definition {
	additional := false
	if driver.operation == checkpointToolName {
		return tool.Definition{
			Name: checkpointToolName, Description: "Create one context checkpoint before expensive exploration. Later call rewind with concise findings; the engine removes every intermediate checkpoint message from active model history and retains only the checkpoint plus report. Only one checkpoint may be active, and rewind is mandatory before finishing.",
			InputSchema: tool.Schema{Type: "object", Required: []string{"goal"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{"goal": {Type: "string", Description: "investigation goal"}}},
			EffectType:  tool.EffectReadOnly, RiskLevel: "low", PolicyTags: []string{"checkpoint", "context"}, Concurrency: tool.ConcurrencyExclusive, ConcurrencyGroup: "context-checkpoint",
		}
	}
	return tool.Definition{
		Name: rewindToolName, Description: "End the active context checkpoint. Rewind model history to the checkpoint and replace intermediate exploration with the supplied concise findings report. This changes context only; it never reverts workspace files.",
		InputSchema: tool.Schema{Type: "object", Required: []string{"report"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{"report": {Type: "string", Description: "concise investigation findings"}}},
		EffectType:  tool.EffectReadOnly, RiskLevel: "low", PolicyTags: []string{"checkpoint", "context"}, Concurrency: tool.ConcurrencyExclusive, ConcurrencyGroup: "context-checkpoint",
	}
}

func (driver *checkpointDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	if driver == nil || driver.controller == nil {
		return checkpointError(call, errors.New("context checkpoint runtime is unavailable")), nil
	}
	if driver.operation == checkpointToolName {
		var input struct {
			Goal string `json:"goal"`
		}
		if err := json.Unmarshal(call.Arguments, &input); err != nil {
			return checkpointError(call, err), nil
		}
		input.Goal = strings.TrimSpace(input.Goal)
		if input.Goal == "" || len([]rune(input.Goal)) > 4000 {
			return checkpointError(call, errors.New("goal is required and must not exceed 4000 characters")), nil
		}
		startedAt, err := driver.controller.begin(input.Goal)
		if err != nil {
			return checkpointError(call, err), nil
		}
		structured, _ := json.Marshal(map[string]any{"goal": input.Goal, "startedAt": startedAt})
		return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "Checkpoint: " + input.Goal + "\nFinish exploration and formulate findings, then call rewind.", Structured: structured}, nil
	}
	var input struct {
		Report string `json:"report"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return checkpointError(call, err), nil
	}
	input.Report = strings.TrimSpace(input.Report)
	if input.Report == "" || len([]rune(input.Report)) > 32_000 {
		return checkpointError(call, errors.New("report is required and must not exceed 32000 characters")), nil
	}
	if err := driver.controller.requestRewind(input.Report); err != nil {
		return checkpointError(call, err), nil
	}
	structured, _ := json.Marshal(map[string]any{"report": input.Report, "rewound": true})
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "Rewind requested. Report captured for context replacement.", Structured: structured}, nil
}

func (controller *checkpointController) begin(goal string) (time.Time, error) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.state.Active {
		return time.Time{}, errors.New("Checkpoint already active")
	}
	now := time.Now().UTC()
	controller.state = persistedCheckpointState{Version: 1, Active: true, Goal: goal, StartedAt: now}
	controller.pendingReport = ""
	return now, nil
}

func (controller *checkpointController) requestRewind(report string) error {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if !controller.state.Active {
		if controller.state.Report != "" {
			return errors.New("Checkpoint already completed; continue from the retained rewind report instead of calling rewind again")
		}
		return errors.New("No active checkpoint. Create a checkpoint before calling rewind")
	}
	if len(controller.state.Snapshot) == 0 {
		return errors.New("Checkpoint snapshot is not durable yet; wait for the checkpoint tool result before rewinding")
	}
	controller.pendingReport = report
	return nil
}

func (controller *checkpointController) Apply(ctx context.Context, history []message.Message, results []tool.Result) ([]message.Message, error) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	for _, result := range results {
		if result.IsError {
			continue
		}
		switch result.Name {
		case checkpointToolName:
			if !controller.state.Active {
				continue
			}
			controller.state.Snapshot = cloneCheckpointMessages(history)
			if err := controller.persistLocked(ctx); err != nil {
				return nil, err
			}
		case rewindToolName:
			if !controller.state.Active || controller.pendingReport == "" || len(controller.state.Snapshot) == 0 {
				continue
			}
			report := controller.pendingReport
			controller.pendingReport = ""
			base := cloneCheckpointMessages(controller.state.Snapshot)
			value := message.NewText(message.RoleSystem, "Checkpoint called and rewound. Report retained below. Need explore again → create a new checkpoint.\n\nReport:\n"+report)
			value.Visibility = message.VisibilityPrivate
			history = append(base, value)
			controller.state.Active = false
			controller.state.Report = report
			controller.state.RewoundAt = time.Now().UTC()
			controller.state.Snapshot = nil
			if err := controller.persistLocked(ctx); err != nil {
				return nil, err
			}
		}
	}
	return cloneCheckpointMessages(history), nil
}

func (controller *checkpointController) persistLocked(ctx context.Context) error {
	if controller.store == nil {
		return nil
	}
	controller.state.Version = 1
	controller.state.SavedAt = time.Now().UTC()
	payload, err := json.Marshal(controller.state)
	if err != nil {
		return err
	}
	if _, err := controller.store.PutArtifact(ctx, controller.sessionID, controller.runID, checkpointArtifactKind, payload, ""); err != nil {
		return fmt.Errorf("persist context checkpoint: %w", err)
	}
	return nil
}

func (controller *checkpointController) active() bool {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.state.Active
}

func (controller *checkpointController) TransformContext(_ context.Context, messages []message.Message) ([]message.Message, error) {
	if !controller.active() {
		return messages, nil
	}
	value := message.NewText(message.RoleSystem, "Exploration checkpoint active. MUST call rewind with concise findings before yielding or finishing. Do not create another checkpoint while this one is active.")
	value.Visibility = message.VisibilityPrivate
	return append(cloneCheckpointMessages(messages), value), nil
}
func (*checkpointController) BeforeModelCall(context.Context, *hyprovider.Request) error { return nil }
func (*checkpointController) BeforeToolCall(context.Context, *tool.Call) error           { return nil }
func (*checkpointController) AfterToolCall(context.Context, *tool.Result) error          { return nil }
func (*checkpointController) OnEvent(context.Context, hyprovider.Event) error            { return nil }

func (controller *checkpointController) guardrail() hyagent.OutputGuardrail {
	return hyagent.NewOutputGuardrail("active-checkpoint", func(_ context.Context, _ hyagent.OutputGuardrailInput) (hyagent.OutputGuardrailResult, error) {
		if !controller.active() {
			return hyagent.AllowOutput(), nil
		}
		value := message.NewText(message.RoleSystem, "Exploration checkpoint remains active. Call rewind now with the concise findings report before finishing.")
		value.Visibility = message.VisibilityPrivate

		return hyagent.RetryOutput(value), nil
	})
}

func cloneCheckpointMessages(values []message.Message) []message.Message {
	cloned := make([]message.Message, len(values))
	for index := range values {
		cloned[index] = message.Clone(values[index])
	}
	return cloned
}

func checkpointError(call tool.Call, err error) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: call.Name + " failed: " + err.Error(), IsError: true}
}
