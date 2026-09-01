package agentruntime

import (
	"context"
	"time"
)

type RunStore interface {
	SaveRun(context.Context, Run) error
	LoadRun(context.Context, string) (Run, error)
	// ListRuns filters runs by RunSelector. All set selector fields
	// AND-combine. Pagination beyond Limit is provider's choice (cursor /
	// offset / none).
	//
	// Spec anchor: docs/product-spec/v0.8.0/05-storage.md §"RunSelector".
	ListRuns(context.Context, RunSelector) ([]Run, error)
}

type TaskStore interface {
	SaveTask(context.Context, Task) error
	LoadTask(context.Context, string, string) (Task, error)
	ListTasks(context.Context, string) ([]Task, error)
}

type EventStore interface {
	AppendEvent(context.Context, Event) error
	ListEvents(context.Context, string) ([]Event, error)
	// ListAfter returns events with Sequence > afterSeq within the same
	// run, in Sequence order. This is the partial-replay primitive: the
	// runtime calls ListAfter(runID, lastSeenSeq) to resume.
	//
	// Spec anchor: docs/product-spec/v0.8.0/05-storage.md §"Event ordering".
	ListAfter(ctx context.Context, runID string, afterSeq uint64) ([]Event, error)
}

type TraceStore interface {
	SaveTraceSpan(context.Context, TraceSpan) error
	ListTraceSpans(context.Context, string) ([]TraceSpan, error)
}

type TraceSpanUpdater interface {
	LoadTraceSpan(context.Context, string) (TraceSpan, error)
	UpdateTraceSpan(context.Context, TraceSpan) error
}

type BlackboardReadWriter interface {
	WriteItem(context.Context, BlackboardItem) error
	SelectItems(context.Context, string, BlackboardSelector) ([]BlackboardItem, error)
}

type BlackboardCommittedReader interface {
	SelectItems(context.Context, string, BlackboardSelector) ([]BlackboardItem, error)
}

type BlackboardSubscriber interface {
	Subscribe(context.Context, string, BlackboardSelector) (<-chan BlackboardItem, func() error, error)
}

type BlackboardWaiter interface {
	WaitForBlackboard(context.Context, string, BlackboardSelector, func([]BlackboardItem) bool, time.Duration) ([]BlackboardItem, error)
}

type UserMessageStore interface {
	QueueMessage(context.Context, UserMessage) error
	LoadMessage(context.Context, string, string) (UserMessage, error)
	UpdateMessage(context.Context, UserMessage) error
	ListMessages(context.Context, string) ([]UserMessage, error)
	// ListPendingFor restricts to one recipient / run; FIFO within the
	// returned slice. Messages queued in order T1 < T2 MUST appear in
	// the same order.
	//
	// Spec anchor: docs/product-spec/v0.8.0/05-storage.md §"Outbox FIFO".
	ListPendingFor(context.Context, UserMessageSelector) ([]UserMessage, error)
}

type UserMessageOutboxScanner interface {
	ListQueuedMessages(context.Context) ([]UserMessage, error)
}

type MailboxOutboxStore interface {
	QueueEnvelope(context.Context, TaskEnvelope) error
	LoadEnvelope(context.Context, string) (TaskEnvelope, error)
	UpdateEnvelope(context.Context, TaskEnvelope) error
	ListEnvelopes(context.Context, string) ([]TaskEnvelope, error)
}

type LeaseStore interface {
	SaveLease(context.Context, TaskExecutionLease) error
	LoadLease(context.Context, string) (TaskExecutionLease, error)
	// ActiveLeaseForTask returns the latest lease slot for the task. The
	// returned lease may be released or expired; callers must inspect Status
	// and ExpiresAt when deciding whether it is active.
	ActiveLeaseForTask(context.Context, string, string) (TaskExecutionLease, bool, error)
	// AcquireWithExpectedVersion atomically persists lease if and only if the
	// latest lease for the same (RunID, TaskID) has Version ==
	// expectedVersion and no unexpired active lease exists. A successful
	// acquire sets Version to expectedVersion+1. Returns (false, nil) on a
	// version mismatch or active holder. MUST be atomic: concurrent acquires
	// for one task, even with different lease IDs, have exactly one winner.
	//
	// Spec anchor: docs/product-spec/v0.8.0/05-storage.md §"Lease CAS — the
	// critical contract".
	AcquireWithExpectedVersion(ctx context.Context, lease TaskExecutionLease, expectedVersion uint64) (bool, error)
	// ExtendLease atomically advances the lease expiry if and only if leaseID
	// is the task's latest active, unexpired lease and its current holder
	// equals workerID. Returns (false, nil) otherwise.
	ExtendLease(ctx context.Context, leaseID string, workerID string, newExpiry time.Time) (bool, error)
	// ReleaseExpiredLease atomically releases leaseID if and only if it is
	// still the latest active lease for its task, its Version equals
	// expectedVersion, and its expiry is not after releasedAt. A successful
	// release increments Version. Returns (false, nil) when any condition no
	// longer holds.
	ReleaseExpiredLease(ctx context.Context, leaseID string, expectedVersion uint64, releasedAt time.Time) (bool, error)
}

type ApprovalStore interface {
	SaveApproval(context.Context, ApprovalRequest) error
	LoadApproval(context.Context, string) (ApprovalRequest, error)
}

type ResumeTokenStore interface {
	SaveResumeToken(context.Context, ResumeToken) error
	LoadResumeToken(context.Context, string) (ResumeToken, error)
	// ListPending returns resume tokens that have not yet been consumed.
	// Providers that report SupportsListPending=false MAY return an empty
	// slice and the runtime falls back to a polling path.
	//
	// Spec anchor: docs/product-spec/v0.8.0/05-storage.md §"Resume token
	// enumeration".
	ListPending(context.Context, ResumeTokenSelector) ([]ResumeToken, error)
}

type ActionAttemptStore interface {
	// SaveActionAttempt MUST enforce uniqueness for non-empty
	// (RunID, TaskID, ToolName, IdempotencyKey) tuples.
	SaveActionAttempt(context.Context, ActionAttempt) error
	LoadActionAttempt(context.Context, string) (ActionAttempt, error)
	LoadActionAttemptByIdempotencyKey(ctx context.Context, runID string, taskID string, toolName string, key string) (ActionAttempt, error)
	ListActionAttempts(context.Context, ActionAttemptSelector) ([]ActionAttempt, error)
	// ResolveActionAttempt atomically replaces an unknown reconciliation
	// candidate. It succeeds only when the stored attempt has the same ID,
	// Status == ActionAttemptUnknown, and RequiresReconcile == true. The
	// replacement must preserve identity fields, set a terminal non-unknown
	// status, and clear RequiresReconcile. Returns false on a compare-and-swap
	// conflict.
	ResolveActionAttempt(context.Context, ActionAttempt) (bool, error)
}

type DurableAttemptRepository interface {
	ListDurableReconcileAttempts(context.Context) ([]ActionAttempt, error)
	LoadDurableReconcileAttempt(context.Context, string) (ActionAttempt, error)
}

// AgentProfileStore persists the framework-level identity of agents.
// AgentProfile.ID is the natural key; Save MUST be idempotent on ID.
//
// Spec anchor: docs/product-spec/v0.8.0/03-agent-ontology.md.
type AgentProfileStore interface {
	SaveAgentProfile(context.Context, AgentProfile) error
	LoadAgentProfile(context.Context, string) (AgentProfile, error)
	ListAgentProfiles(context.Context, AgentSelector) ([]AgentProfile, error)
}

// CapabilityStore persists Capability declarations. Capability.Name +
// Capability.AgentID is the natural key.
//
// Spec anchor: docs/product-spec/v0.8.0/03-agent-ontology.md.
type CapabilityStore interface {
	SaveCapability(context.Context, Capability) error
	LoadCapability(ctx context.Context, name string, agentID string) (Capability, error)
	ListCapabilities(context.Context, CapabilitySelector) ([]Capability, error)
}

// DeadLetterStore captures envelopes that exhausted retries. Requeue is
// OPTIONAL and gated on StoreCapabilities.SupportsDeadLetterRequeue;
// providers that report false MAY return an error from Requeue.
//
// Spec anchor: docs/product-spec/v0.8.0/04-worker-runtime.md.
type DeadLetterStore interface {
	AppendDeadLetter(context.Context, DeadLetterEntry) error
	ListDeadLetters(context.Context, DeadLetterSelector) ([]DeadLetterEntry, error)
	Requeue(ctx context.Context, deadLetterID string) error
}

// HandoffStore persists typed multi-agent handoff records. SaveHandoff is
// append-only — a second save with an existing (RunID, ID) returns an
// error; there is no update path. ListHandoffs MUST return matches in
// ID-ascending order: IDs are scheduler-derived ULIDs, so ascending ID is
// wall-clock order per tick and replay observes handoffs
// deterministically regardless of persistence order.
//
// Spec anchor: docs/product-spec/v0.8.0/07-storage.md §"HandoffStore".
type HandoffStore interface {
	SaveHandoff(context.Context, HandoffRecord) error
	LoadHandoff(ctx context.Context, runID, handoffID string) (HandoffRecord, error)
	ListHandoffs(context.Context, HandoffSelector) ([]HandoffRecord, error)
}

// TeamStateStore persists the latest TeamState snapshot per run.
// SaveTeamState overwrites atomically; LoadTeamState returns ErrNotFound
// when no snapshot exists. Snapshots arrive up to once per scheduler
// tick, so implementations must not lock out readers on write.
//
// Spec anchor: docs/product-spec/v0.8.0/07-storage.md §"TeamStateStore".
type TeamStateStore interface {
	SaveTeamState(context.Context, TeamStateRecord) error
	LoadTeamState(ctx context.Context, runID string) (TeamStateRecord, error)
}

// AgentInstanceStore persists AgentInstance lifecycle rows.
// SaveAgentInstance upserts on ID; the append-only status history is the
// event log.
//
// Spec anchor: docs/product-spec/v0.8.0/07-storage.md
// §"AgentInstanceStore".
type AgentInstanceStore interface {
	SaveAgentInstance(context.Context, AgentInstanceRecord) error
	LoadAgentInstance(ctx context.Context, id string) (AgentInstanceRecord, error)
	ListAgentInstances(context.Context, AgentInstanceSelector) ([]AgentInstanceRecord, error)
}

// RunStores is the durable run / task / event surface of a UnitOfWork.
// Spec anchor: ADR-022.
type RunStores interface {
	Runs() RunStore
	Tasks() TaskStore
	Events() EventStore
}

// CollaborationStores is the multi-agent collaboration surface.
// Spec anchor: ADR-022, ADR-016 §6.
type CollaborationStores interface {
	Blackboard() BlackboardReadWriter
	Handoffs() HandoffStore
	TeamStates() TeamStateStore
	AgentInstances() AgentInstanceStore
}

// MessagingStores is the mailbox and user-message surface.
// Spec anchor: ADR-022.
type MessagingStores interface {
	MailboxOutbox() MailboxOutboxStore
	UserMessages() UserMessageStore
}

// GovernanceStores is the approval, resume, action-attempt, and dead-letter
// surface.
// Spec anchor: ADR-022.
type GovernanceStores interface {
	Approvals() ApprovalStore
	ResumeTokens() ResumeTokenStore
	ActionAttempts() ActionAttemptStore
	DeadLetters() DeadLetterStore
}

// IdentityStores is the agent-profile and capability catalog surface.
// Spec anchor: ADR-022.
type IdentityStores interface {
	AgentProfiles() AgentProfileStore
	CapabilityCatalog() CapabilityStore
}

// ObservabilityStores is the trace and lease surface.
// Spec anchor: ADR-022.
type ObservabilityStores interface {
	Trace() TraceStore
	Leases() LeaseStore
}

// UnitOfWork is the composite store transaction. It embeds the capability
// interfaces so callers may accept a narrower type. Host providers still
// implement the full composite. Spec anchor: ADR-022.
type UnitOfWork interface {
	RunStores
	CollaborationStores
	MessagingStores
	GovernanceStores
	IdentityStores
	ObservabilityStores
	Commit(context.Context) error
	Rollback(context.Context) error
}

type StoreProvider interface {
	Begin(context.Context) (UnitOfWork, error)
}

// StoreCapabilities is the provider's self-declaration of optional features.
// The runtime branches on these flags rather than probing the provider. A
// provider that returns false for an optional capability is valid. Polling,
// single-writer, and non-requeue fallbacks cover the corresponding operational
// features; admission reservations and resource claims instead reject calls
// that require their disabled contract.
// Providers that do not implement CapabilityReporter are treated as having
// DefaultStoreCapabilities (the conservative profile).
type StoreCapabilities struct {
	SupportsTransactions          bool `json:"supportsTransactions"`          // UoW commit/rollback are atomic
	SupportsBlackboardSubscribe   bool `json:"supportsBlackboardSubscribe"`   // can push Blackboard items without polling
	SupportsListPending           bool `json:"supportsListPending"`           // ResumeTokens / Outbox enumeration
	SupportsConcurrentWriters     bool `json:"supportsConcurrentWriters"`     // safe under N>1 worker processes
	SupportsDeadLetterRequeue     bool `json:"supportsDeadLetterRequeue"`     // dead-lettered envelopes can be re-queued
	SupportsAdmissionReservations bool `json:"supportsAdmissionReservations"` // atomic aggregate-capacity reservations
	SupportsResourceClaims        bool `json:"supportsResourceClaims"`        // atomic shared/exclusive coordination claims
}

// DefaultStoreCapabilities is the conservative profile applied to providers
// that do not implement CapabilityReporter. It enables polling and serialized
// worker fallbacks while rejecting definition, admission, or resource-claim
// features that require an unadvertised durable contract.
func DefaultStoreCapabilities() StoreCapabilities {
	return StoreCapabilities{
		SupportsTransactions:          false,
		SupportsBlackboardSubscribe:   false,
		SupportsListPending:           false,
		SupportsConcurrentWriters:     false,
		SupportsDeadLetterRequeue:     false,
		SupportsAdmissionReservations: false,
		SupportsResourceClaims:        false,
	}
}

// CapabilityReporter is an OPTIONAL StoreProvider extension. Providers that
// implement this interface report which optional features they support so
// the runtime can branch deterministically rather than probing. Providers
// that do not implement this interface are treated as having
// DefaultStoreCapabilities.
//
// Spec anchor: docs/product-spec/v0.8.0/05-storage.md §"Capability reporting".
type CapabilityReporter interface {
	Capabilities(ctx context.Context) (StoreCapabilities, error)
}

// ProviderCloser is an OPTIONAL StoreProvider extension. Providers that
// implement this interface receive a clean shutdown hook from the runtime.
// Providers that do not implement this interface are simply abandoned —
// callers may still close pools or connections via provider-specific APIs.
//
// Spec anchor: docs/product-spec/v0.8.0/05-storage.md §"Top-level provider".
type ProviderCloser interface {
	Close(ctx context.Context) error
}

// LeaseCAS is kept as a documentary alias of the lease CAS contract; the
// underlying methods are now REQUIRED on LeaseStore. External code that
// needs to identify the CAS contract specifically can use this alias for
// readability — but every LeaseStore implementation already satisfies it.
//
// Spec anchor: docs/product-spec/v0.8.0/05-storage.md §"Lease CAS — the
// critical contract".
type LeaseCAS interface {
	AcquireWithExpectedVersion(ctx context.Context, lease TaskExecutionLease, expectedVersion uint64) (bool, error)
	ExtendLease(ctx context.Context, leaseID string, workerID string, newExpiry time.Time) (bool, error)
}
