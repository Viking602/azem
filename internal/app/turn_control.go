package app

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

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
	InterruptStream       bool
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
// Trusted stream guards can end text-only generation at the next event boundary.
type turnControlRuntime struct {
	queue *turnControlQueue

	mu           sync.Mutex
	observed     []string
	deadlineAt   time.Time
	wrapUpAt     time.Time
	wrapUpQueued bool
}

func bindTurnControl(engine hyagent.Engine, queue *turnControlQueue, deadlineAt time.Time) hyagent.Engine {
	if queue == nil {
		return engine
	}
	runtime := &turnControlRuntime{queue: queue, deadlineAt: deadlineAt}
	if !deadlineAt.IsZero() {
		runtime.wrapUpAt = deadlineAt.Add(-min(90*time.Second, max(0, time.Until(deadlineAt)/5)))
	}
	engine.Hooks = engine.Hooks.Prepend(runtime)
	engine.OutputGuardrails = append([]hyagent.OutputGuardrail{runtime}, engine.OutputGuardrails...)
	engine.ModelInterceptor = hyprovider.ChainStreamInterceptors(hyprovider.StreamInterceptorFunc(func(ctx context.Context, next hyprovider.Driver, request hyprovider.Request) (hyprovider.Stream, error) {
		stream, err := next.Stream(ctx, request)
		if err != nil {
			return nil, err
		}
		identity := hyprovider.StreamIdentity{Provider: next.Metadata(), Model: request.Model}
		if identified, ok := stream.(hyprovider.IdentifiedStream); ok {
			identity = identified.Identity()
		}
		return &turnControlStream{Stream: stream, ctx: ctx, queue: queue, identity: identity}, nil
	}), engine.ModelInterceptor)
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

type turnControlStream struct {
	hyprovider.Stream
	ctx         context.Context
	queue       *turnControlQueue
	identity    hyprovider.StreamIdentity
	unsafeToCut bool
	interrupted bool
}

func (stream *turnControlStream) Identity() hyprovider.StreamIdentity { return stream.identity }

func (stream *turnControlStream) Recv() (hyprovider.Event, error) {
	if stream.interrupted {
		return hyprovider.Event{}, io.EOF
	}
	if !stream.unsafeToCut && stream.ctx.Err() == nil && stream.queue.shouldInterruptStream() {
		if err := stream.Stream.Close(); err != nil {
			return hyprovider.Event{}, err
		}
		stream.interrupted = true
		// This is an explicit host abort, never a fabricated provider completion.
		// The pending control makes the output guardrail continue the same run.
		return hyprovider.Event{Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonAborted}, nil
	}
	event, err := stream.Stream.Recv()
	switch event.Kind {
	case hyprovider.EventToolCallDelta, hyprovider.EventToolCall, hyprovider.EventDone, hyprovider.EventError:
		// Preserve partial tool calls, provider-side exec effects and real terminals.
		stream.unsafeToCut = true
	}
	return event, err
}

func (stream *turnControlStream) Close() error {
	if stream.interrupted {
		return nil
	}
	return stream.Stream.Close()
}

func (queue *turnControlQueue) shouldInterruptStream() bool {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	for _, control := range queue.pending {
		if control.InterruptStream {
			if _, reserved := queue.reserved[control.ID]; !reserved {
				return true
			}
		}
	}
	return false
}

func (*turnControlRuntime) TransformContext(_ context.Context, messages []message.Message) ([]message.Message, error) {
	return messages, nil
}

func (runtime *turnControlRuntime) BeforeModelCall(ctx context.Context, request *hyprovider.Request) error {
	if err := runtime.enqueueDeadlineWrapUp(ctx); err != nil {
		return err
	}
	batch, err := runtime.queue.Drain(ctx, turnControlBeforeModel)
	if err != nil {
		return err
	}
	request.Messages = append(request.Messages, turnControlMessages(batch)...)
	return nil
}

func (*turnControlRuntime) BeforeToolCall(context.Context, *tool.Call) error  { return nil }
func (*turnControlRuntime) AfterToolCall(context.Context, *tool.Result) error { return nil }
func (runtime *turnControlRuntime) OnEvent(ctx context.Context, event hyprovider.Event) error {
	// Leave final prose and provider terminal events alone. The stream wrapper
	// also defers interruption once a tool call or provider-side effect starts.
	if event.Kind == hyprovider.EventThinkingDelta {
		return runtime.enqueueDeadlineWrapUp(ctx)
	}
	return nil
}

func (runtime *turnControlRuntime) enqueueDeadlineWrapUp(ctx context.Context) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	now := time.Now()
	if runtime.wrapUpQueued || runtime.deadlineAt.IsZero() || ctx.Err() != nil ||
		now.Before(runtime.wrapUpAt) || !now.Before(runtime.deadlineAt) {
		return nil
	}
	text := "[Host deadline: wrap up]\n" + runtimeDeadlineContext(ctx, runtime.deadlineAt) +
		"\nSave the current best deliverable to the required files now if the task requires file outputs; otherwise finish the requested response. " +
		"If required checks already pass, preserve the working result and finish. " +
		"Stop optional optimization and repeated analysis. Use tools for necessary completion work, " +
		"and report unresolved limitations honestly."
	if err := runtime.queue.Enqueue(turnControlMessage{
		Kind: turnControlSteer, Message: privateTurnControlMessage(text), InterruptStream: true,
	}); err != nil {
		return err
	}
	runtime.wrapUpQueued = true
	return nil
}

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
