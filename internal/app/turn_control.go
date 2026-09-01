package app

import (
	"context"
	"fmt"
	"sync"

	"github.com/Viking602/azem/internal/agentruntime"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"
)

type turnControlKind string

const (
	turnControlSteer    turnControlKind = "steer"
	turnControlFollowUp turnControlKind = "follow_up"
)

type turnControlBoundary string

const (
	turnControlBeforeModel turnControlBoundary = "before_model"
	turnControlAfterAnswer turnControlBoundary = "after_answer"
)

const (
	turnControlIDMetadataKey   = "azem.control.id"
	turnControlKindMetadataKey = "azem.control.kind"
)

type turnControlMessage struct {
	ID                    string
	Kind                  turnControlKind
	Message               message.Message
	DiscardRejectedOutput bool
}

// turnControlQueue is an app-owned FIFO. Drain reserves matching messages;
// Acknowledge removes only effects accepted at a later durable boundary, while
// Release makes an uncommitted attempt available again.
type turnControlQueue struct {
	mu       sync.Mutex
	pending  []turnControlMessage
	reserved map[string]struct{}
	nextID   uint64
}

func newTurnControlQueue() *turnControlQueue {
	return &turnControlQueue{reserved: make(map[string]struct{})}
}

func (queue *turnControlQueue) Enqueue(control turnControlMessage) error {
	if queue == nil {
		return fmt.Errorf("turn control queue is nil")
	}
	if control.Kind != turnControlSteer && control.Kind != turnControlFollowUp {
		return fmt.Errorf("unknown turn control kind %q", control.Kind)
	}
	if control.Message.Role == "" {
		return fmt.Errorf("turn control %s requires a message", control.Kind)
	}
	control.Message = message.Clone(control.Message)
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if control.ID == "" {
		queue.nextID++
		control.ID = fmt.Sprintf("control-%d", queue.nextID)
	}
	for _, pending := range queue.pending {
		if pending.ID == control.ID {
			return fmt.Errorf("duplicate turn control id %q", control.ID)
		}
	}
	queue.pending = append(queue.pending, control)
	return nil
}

func (queue *turnControlQueue) Drain(_ context.Context, boundary turnControlBoundary) ([]turnControlMessage, error) {
	if queue == nil {
		return nil, nil
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	selected := make([]turnControlMessage, 0)
	for _, control := range queue.pending {
		if _, reserved := queue.reserved[control.ID]; reserved || !deliverTurnControlAt(control.Kind, boundary) {
			continue
		}
		queue.reserved[control.ID] = struct{}{}
		control.Message = message.Clone(control.Message)
		selected = append(selected, control)
	}
	return selected, nil
}

func deliverTurnControlAt(kind turnControlKind, boundary turnControlBoundary) bool {
	if boundary == turnControlAfterAnswer {
		return true
	}
	return kind == turnControlSteer
}

func (queue *turnControlQueue) Acknowledge(_ context.Context, ids []string) error {
	if queue == nil || len(ids) == 0 {
		return nil
	}
	targets := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		targets[id] = struct{}{}
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	remaining := queue.pending[:0]
	for _, control := range queue.pending {
		if _, acknowledged := targets[control.ID]; acknowledged {
			delete(queue.reserved, control.ID)
			continue
		}
		remaining = append(remaining, control)
	}
	for index := len(remaining); index < len(queue.pending); index++ {
		queue.pending[index] = turnControlMessage{}
	}
	queue.pending = remaining
	return nil
}

func (queue *turnControlQueue) Release(_ context.Context, ids []string) error {
	if queue == nil || len(ids) == 0 {
		return nil
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	for _, id := range ids {
		delete(queue.reserved, id)
	}
	return nil
}

func (queue *turnControlQueue) isReserved(id string) bool {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	_, ok := queue.reserved[id]
	return ok
}

func turnControlMessages(batch []turnControlMessage) []message.Message {
	messages := make([]message.Message, 0, len(batch))
	for _, control := range batch {
		current := message.Clone(control.Message)
		if current.Metadata == nil {
			current.Metadata = make(map[string]string, 2)
		}
		current.Metadata[turnControlIDMetadataKey] = control.ID
		current.Metadata[turnControlKindMetadataKey] = string(control.Kind)
		messages = append(messages, current)
	}
	return messages
}

// turnControlRuntime maps the app FIFO onto v0.16 extension points. Steers are
// appended immediately before a model effect. Controls arriving during that
// effect are consumed by the terminal guardrail and continue as a follow-up.
type turnControlRuntime struct {
	queue *turnControlQueue

	mu       sync.Mutex
	observed []string
}

func bindTurnControl(engine hyagent.Engine, queue *turnControlQueue) hyagent.Engine {
	if queue == nil {
		return engine
	}
	runtime := &turnControlRuntime{queue: queue}
	engine.Hooks = engine.Hooks.Prepend(runtime)
	engine.OutputGuardrails = append(engine.OutputGuardrails, runtime)
	engine.Boundaries = hyagent.JoinBoundaryObservers(engine.Boundaries, runtime)
	prior := engine.StepObserver
	engine.StepObserver = hyagent.StepObserverFunc(func(ctx context.Context, step hyagent.Step) error {
		if prior != nil {
			if err := prior.ObserveStep(ctx, step); err != nil {
				return err
			}
		}
		return runtime.acknowledgeObserved(ctx)
	})
	return engine
}

func (*turnControlRuntime) TransformContext(_ context.Context, messages []message.Message) ([]message.Message, error) {
	return messages, nil
}

func (runtime *turnControlRuntime) BeforeModelCall(ctx context.Context, request *hyprovider.Request) error {
	batch, err := runtime.queue.Drain(ctx, turnControlBeforeModel)
	if err != nil {
		return err
	}
	request.Messages = append(request.Messages, turnControlMessages(batch)...)
	return nil
}

func (*turnControlRuntime) BeforeToolCall(context.Context, *tool.Call) error  { return nil }
func (*turnControlRuntime) AfterToolCall(context.Context, *tool.Result) error { return nil }
func (*turnControlRuntime) OnEvent(context.Context, hyprovider.Event) error   { return nil }

func (*turnControlRuntime) Name() string { return "turn-control" }

func (runtime *turnControlRuntime) Check(ctx context.Context, _ hyagent.OutputGuardrailInput) (hyagent.OutputGuardrailResult, error) {
	batch, err := runtime.queue.Drain(ctx, turnControlAfterAnswer)
	if err != nil {
		return hyagent.OutputGuardrailResult{}, err
	}
	messages := turnControlMessages(batch)
	if len(messages) == 0 {
		return hyagent.AllowOutput(), nil
	}
	includeRejectedOutput := true
	for _, control := range batch {
		if control.DiscardRejectedOutput {
			includeRejectedOutput = false
			break
		}
	}
	policy := hyagent.RetryPolicy{IncludeRejectedOutput: includeRejectedOutput}
	if !includeRejectedOutput {
		discarded := message.NewText(message.RoleAssistant, "[Rejected assistant output discarded by trusted stream control.]")
		agentruntime.SetMessageVisibility(&discarded, agentruntime.MessageVisibilityPrivate)
		policy.ReplacementContext = []message.Message{discarded}
	}
	return hyagent.RetryOutputWithPolicy(policy, messages...), nil
}

func (runtime *turnControlRuntime) ObserveBoundary(_ context.Context, continuation hyagent.Continuation) error {
	seen := make([]string, 0)
	known := make(map[string]struct{})
	for _, current := range continuation.Messages {
		id := current.Metadata[turnControlIDMetadataKey]
		if id == "" || !runtime.queue.isReserved(id) {
			continue
		}
		if _, duplicate := known[id]; duplicate {
			continue
		}
		known[id] = struct{}{}
		seen = append(seen, id)
	}
	runtime.mu.Lock()
	runtime.observed = seen
	runtime.mu.Unlock()
	return nil
}

func (runtime *turnControlRuntime) acknowledgeObserved(ctx context.Context) error {
	runtime.mu.Lock()
	ids := append([]string(nil), runtime.observed...)
	runtime.mu.Unlock()
	if err := runtime.queue.Acknowledge(ctx, ids); err != nil {
		return err
	}
	runtime.mu.Lock()
	if len(ids) == len(runtime.observed) {
		runtime.observed = nil
	}
	runtime.mu.Unlock()
	return nil
}

func privateTurnControlMessage(text string) message.Message {
	value := message.NewText(message.RoleSystem, text)
	agentruntime.SetMessageVisibility(&value, agentruntime.MessageVisibilityPrivate)
	return value
}
