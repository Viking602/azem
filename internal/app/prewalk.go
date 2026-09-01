package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/agentruntime"

	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"
)

const prewalkArtifactKind = session.InternalArtifactKindPrefix + "prewalk-handoff-v1"

type prewalkSwitchDriver struct {
	mu              sync.RWMutex
	initial         hyprovider.Driver
	target          hyprovider.Driver
	targetProvider  string
	targetModel     string
	targetReasoning string
	switched        bool
}

func (driver *prewalkSwitchDriver) Metadata() hyprovider.Metadata {
	metadata := driver.initial.Metadata()
	for _, model := range driver.target.Metadata().Models {
		if !containsString(metadata.Models, model) {
			metadata.Models = append(metadata.Models, model)
		}
	}
	return metadata
}

func (driver *prewalkSwitchDriver) Stream(ctx context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	driver.mu.RLock()
	switched := driver.switched
	target, model, reasoning := driver.target, driver.targetModel, driver.targetReasoning
	initial := driver.initial
	driver.mu.RUnlock()
	if !switched {
		return initial.Stream(ctx, request)
	}
	request.Model = model
	if request.Metadata == nil {
		request.Metadata = make(map[string]string)
	}
	request.Metadata["reasoning_effort"] = reasoning
	request.Metadata["prewalk_target_provider"] = driver.targetProvider
	return target.Stream(ctx, request)
}

func (driver *prewalkSwitchDriver) switchOnce() bool {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	if driver.switched {
		return false
	}
	driver.switched = true
	return true
}

func (driver *prewalkSwitchDriver) isSwitched() bool {
	driver.mu.RLock()
	defer driver.mu.RUnlock()
	return driver.switched
}

type prewalkHook struct {
	driver     *prewalkSwitchDriver
	control    *turnControlQueue
	store      *session.Service
	sessionID  string
	runID      string
	planNudged bool
}

func (hook *prewalkHook) TransformContext(_ context.Context, messages []message.Message) ([]message.Message, error) {
	return messages, nil
}
func (*prewalkHook) BeforeModelCall(context.Context, *hyprovider.Request) error { return nil }
func (*prewalkHook) BeforeToolCall(context.Context, *tool.Call) error           { return nil }
func (*prewalkHook) OnEvent(context.Context, hyprovider.Event) error            { return nil }

func (hook *prewalkHook) AfterToolCall(ctx context.Context, result *tool.Result) error {
	if hook == nil || hook.driver == nil || hook.driver.isSwitched() || result == nil || result.IsError || !prewalkMutationResult(*result) {
		return nil
	}
	if hook.store != nil {
		todo, err := hook.store.LoadTodo(ctx, hook.sessionID)
		if err != nil {
			return err
		}
		if len(todo.Phases) == 0 {
			if !hook.planNudged {
				hook.planNudged = true
				if err := hook.enqueue("prewalk-plan", "Prewalk requires a durable implementation plan before switching models. Stop further mutation, write the complete execution plan, initialize todo with meaningful implementation/verification items, then continue."); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if !hook.driver.switchOnce() {
		return nil
	}
	if hook.store != nil {
		payload, _ := json.Marshal(map[string]any{"version": 1, "provider": hook.driver.targetProvider, "model": hook.driver.targetModel, "triggerTool": result.Name})
		if _, err := hook.store.PutArtifact(ctx, hook.sessionID, hook.runID, prewalkArtifactKind, payload, ""); err != nil {
			return err
		}
	}
	return hook.enqueue("prewalk-checklist", "Prewalk model handoff complete. Continue implementation on the target model. Before completion verify: every matching callsite stayed consistent; scope did not grow beyond the issue; and the complete relevant test module or behavioral smoke path passes. Do not claim completion until all three checks are observed.")
}

func (hook *prewalkHook) enqueue(prefix, text string) error {
	id, err := randomID(prefix)
	if err != nil {
		return err
	}
	value := message.NewText(message.RoleSystem, text)
	agentruntime.SetMessageVisibility(&value, agentruntime.MessageVisibilityPrivate)
	return hook.control.Enqueue(turnControlMessage{ID: id, Kind: turnControlSteer, Message: value})
}

func prewalkMutationResult(result tool.Result) bool {
	switch result.Name {
	case "coding.write_file", "coding.edit_hashline", "coding.replace", "coding.delete_file":
		return true
	case "coding.gofmt":
		var status struct {
			Changed bool `json:"changed"`
		}
		return json.Unmarshal(result.Structured, &status) == nil && status.Changed
	default:
		return false
	}
}

func (r *ProviderRuntime) preparePrewalkDriver(ctx context.Context, request TurnRequest, run *agentservice.Run, host providerHost, currentAccountID string, current hyprovider.Driver) (hyprovider.Driver, *prewalkSwitchDriver, error) {
	if request.Prewalk == nil {
		return current, nil, nil
	}
	targetRoute := *request.Prewalk
	if targetRoute.Provider == request.Provider && targetRoute.Model == request.Model && (targetRoute.Reasoning == "" || targetRoute.Reasoning == request.Reasoning) {
		return current, nil, nil
	}
	requestedAccountID := ""
	if targetRoute.Provider == request.Provider {
		requestedAccountID = currentAccountID
	}
	account, targetModel, _, targetDriver, err := r.resolveDriverForAccount(ctx, targetRoute.Provider, targetRoute.Model, targetRoute.Reasoning, requestedAccountID)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve prewalk target: %w", err)
	}
	targetReasoning, err := r.resolvedReasoningEffort(ctx, targetRoute.Provider, account.ID, targetModel, targetRoute.Reasoning)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve prewalk reasoning: %w", err)
	}
	if host != nil && host.Sessions() != nil {
		targetDriver = &meteredProviderDriver{
			inner: targetDriver, store: host.Sessions(), host: host, sessionID: request.SessionID, runID: run.RunID,
			kind: "main", provider: targetRoute.Provider, model: targetModel, transport: targetDriver.Metadata().Name,
		}
	}
	targetDriver = retryProviderDriver(ctx, host, request.SessionID, run.RunID, targetRoute.Provider, r.cfg.Retry, targetDriver)
	switcher := &prewalkSwitchDriver{
		initial: current, target: targetDriver, targetProvider: targetRoute.Provider, targetModel: targetModel, targetReasoning: targetReasoning,
	}
	return switcher, switcher, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
