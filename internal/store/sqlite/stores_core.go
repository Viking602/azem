package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
	"github.com/Viking602/go-hydaelyn/api"
)

const (
	kindRun          = "run"
	kindTask         = "task"
	kindTrace        = "trace"
	kindBlackboard   = "blackboard"
	kindUserMessage  = "user_message"
	kindEnvelope     = "envelope"
	kindApproval     = "approval"
	kindResume       = "resume_token"
	kindAction       = "action_attempt"
	kindAgentProfile = "agent_profile"
	kindCapability   = "capability"
	kindUsage        = "usage"
	kindDeadLetter   = "dead_letter"
	kindHandoff      = "handoff"
	kindTeamState    = "team_state"
	kindInstance     = "agent_instance"
)

type unitOfWork struct {
	tx     *sql.Tx
	closed bool
}

func (u *unitOfWork) Runs() api.RunStore                     { return u }
func (u *unitOfWork) Tasks() api.TaskStore                   { return u }
func (u *unitOfWork) Events() api.EventStore                 { return u }
func (u *unitOfWork) Blackboard() api.BlackboardReadWriter   { return u }
func (u *unitOfWork) MailboxOutbox() api.MailboxOutboxStore  { return u }
func (u *unitOfWork) UserMessages() api.UserMessageStore     { return u }
func (u *unitOfWork) Trace() api.TraceStore                  { return u }
func (u *unitOfWork) Leases() api.LeaseStore                 { return u }
func (u *unitOfWork) Approvals() api.ApprovalStore           { return u }
func (u *unitOfWork) ResumeTokens() api.ResumeTokenStore     { return u }
func (u *unitOfWork) ActionAttempts() api.ActionAttemptStore { return u }
func (u *unitOfWork) AgentProfiles() api.AgentProfileStore   { return u }
func (u *unitOfWork) CapabilityCatalog() api.CapabilityStore { return u }
func (u *unitOfWork) UsageRecords() api.UsageStore           { return u }
func (u *unitOfWork) DeadLetters() api.DeadLetterStore       { return u }
func (u *unitOfWork) Handoffs() api.HandoffStore             { return u }
func (u *unitOfWork) TeamStates() api.TeamStateStore         { return u }
func (u *unitOfWork) AgentInstances() api.AgentInstanceStore { return u }

func (u *unitOfWork) Commit(ctx context.Context) error {
	if u.closed {
		return sql.ErrTxDone
	}
	u.closed = true
	if err := ctx.Err(); err != nil {
		_ = u.tx.Rollback()
		return err
	}
	return u.tx.Commit()
}

func (u *unitOfWork) Rollback(context.Context) error {
	if u.closed {
		return sql.ErrTxDone
	}
	u.closed = true
	return u.tx.Rollback()
}

func (u *unitOfWork) SaveRun(ctx context.Context, value api.Run) error {
	return u.save(ctx, kindRun, value.ID, "", value.ID, "", string(value.Status), value.CreatedAt, "", "", value, true)
}

func (u *unitOfWork) LoadRun(ctx context.Context, id string) (api.Run, error) {
	return loadRecord[api.Run](ctx, u.tx, kindRun, id, "")
}

func (u *unitOfWork) ListRuns(ctx context.Context, selector api.RunSelector) ([]api.Run, error) {
	values, err := listRecords[api.Run](ctx, u.tx, kindRun, "")
	if err != nil {
		return nil, err
	}
	filtered := values[:0]
	for _, value := range values {
		if len(selector.IDs) > 0 && !contains(selector.IDs, value.ID) || len(selector.Statuses) > 0 && !contains(selector.Statuses, value.Status) || !within(value.CreatedAt, selector.Since, selector.Until) {
			continue
		}
		if selector.AgentID != "" && value.Metadata["agent_id"] != selector.AgentID || selector.AgentVersion != "" && value.Metadata["agent_version"] != selector.AgentVersion {
			continue
		}
		filtered = append(filtered, value)
	}
	return limit(filtered, selector.Limit), nil
}

func (u *unitOfWork) SaveTask(ctx context.Context, value api.Task) error {
	return u.save(ctx, kindTask, value.ID, value.RunID, value.RunID, value.ID, string(value.Status), value.CreatedAt, "", "", value, true)
}

func (u *unitOfWork) LoadTask(ctx context.Context, runID string, taskID string) (api.Task, error) {
	return loadRecord[api.Task](ctx, u.tx, kindTask, taskID, runID)
}

func (u *unitOfWork) ListTasks(ctx context.Context, runID string) ([]api.Task, error) {
	return listRecords[api.Task](ctx, u.tx, kindTask, runID)
}

func (u *unitOfWork) AppendEvent(ctx context.Context, value api.Event) error {
	queries := dbgen.New(u.tx)
	if value.Sequence <= 0 {
		latest, err := queries.LatestEventSequence(ctx, value.RunID)
		if errors.Is(err, sql.ErrNoRows) {
			latest = 0
		} else if err != nil {
			return fmt.Errorf("allocate event sequence: %w", err)
		}
		if latest == math.MaxInt64 {
			return fmt.Errorf("allocate event sequence: SQLite INTEGER overflow")
		}
		sequence, err := intFromInt64(latest + 1)
		if err != nil {
			return fmt.Errorf("allocate event sequence: %w", err)
		}
		value.Sequence = sequence
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	err = queries.InsertEvent(ctx, dbgen.InsertEventParams{RunID: value.RunID, Sequence: int64(value.Sequence), RecordedAt: nanos(value.RecordedAt), Data: data})
	if err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}

func (u *unitOfWork) ListEvents(ctx context.Context, runID string) ([]api.Event, error) {
	return u.listEventsAfter(ctx, runID, 0, false)
}

func (u *unitOfWork) ListAfter(ctx context.Context, runID string, afterSeq uint64) ([]api.Event, error) {
	return u.listEventsAfter(ctx, runID, afterSeq, true)
}

func (u *unitOfWork) listEventsAfter(ctx context.Context, runID string, afterSeq uint64, strict bool) ([]api.Event, error) {
	queries := dbgen.New(u.tx)
	var data [][]byte
	var err error
	if strict {
		sequence, conversionErr := int64FromUint64(afterSeq)
		if conversionErr != nil {
			return nil, fmt.Errorf("list events: %w", conversionErr)
		}
		data, err = queries.ListEventDataAfter(ctx, dbgen.ListEventDataAfterParams{RunID: runID, Sequence: sequence})
	} else {
		data, err = queries.ListEventData(ctx, runID)
	}
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return decodeRows[api.Event](data)
}

func (u *unitOfWork) SaveTraceSpan(ctx context.Context, value api.TraceSpan) error {
	return u.save(ctx, kindTrace, value.ID, "", value.RunID, value.TaskID, string(value.Status), value.StartedAt, "", "", value, true)
}

func (u *unitOfWork) ListTraceSpans(ctx context.Context, runID string) ([]api.TraceSpan, error) {
	return listRecords[api.TraceSpan](ctx, u.tx, kindTrace, runID)
}

func (u *unitOfWork) WriteItem(ctx context.Context, value api.BlackboardItem) error {
	return u.save(ctx, kindBlackboard, value.ID, "", value.RunID, value.TaskID, string(value.Visibility), value.CreatedAt, "", "", value, true)
}

func (u *unitOfWork) SelectItems(ctx context.Context, runID string, selector api.BlackboardSelector) ([]api.BlackboardItem, error) {
	values, err := listRecords[api.BlackboardItem](ctx, u.tx, kindBlackboard, runID)
	if err != nil {
		return nil, err
	}
	filtered := values[:0]
	for _, value := range values {
		if selector.RunID != "" && value.RunID != selector.RunID || selector.TaskID != "" && value.TaskID != selector.TaskID || len(selector.ItemTypes) > 0 && !contains(selector.ItemTypes, value.Type) || len(selector.SourceTypes) > 0 && !contains(selector.SourceTypes, value.Source.Type) || len(selector.SourceIDs) > 0 && !contains(selector.SourceIDs, value.Source.ID) || len(selector.SourceAgentIDs) > 0 && !contains(selector.SourceAgentIDs, value.Source.ID) || selector.Visibility != "" && value.Visibility != selector.Visibility || selector.SinceVersion > 0 && value.Version <= selector.SinceVersion || len(selector.Keys) > 0 && !contains(selector.Keys, value.Key) {
			continue
		}
		filtered = append(filtered, value)
	}
	return limit(filtered, selector.Limit), nil
}

func (u *unitOfWork) QueueMessage(ctx context.Context, value api.UserMessage) error {
	return u.save(ctx, kindUserMessage, value.ID, value.RunID, value.RunID, value.TaskID, string(value.Status), value.CreatedAt, "", value.IdempotencyKey, value, false)
}

func (u *unitOfWork) LoadMessage(ctx context.Context, runID string, messageID string) (api.UserMessage, error) {
	return loadRecord[api.UserMessage](ctx, u.tx, kindUserMessage, messageID, runID)
}

func (u *unitOfWork) UpdateMessage(ctx context.Context, value api.UserMessage) error {
	return u.save(ctx, kindUserMessage, value.ID, value.RunID, value.RunID, value.TaskID, string(value.Status), value.CreatedAt, "", value.IdempotencyKey, value, true)
}

func (u *unitOfWork) ListMessages(ctx context.Context, runID string) ([]api.UserMessage, error) {
	return listRecords[api.UserMessage](ctx, u.tx, kindUserMessage, runID)
}

func (u *unitOfWork) ListPendingFor(ctx context.Context, selector api.UserMessageSelector) ([]api.UserMessage, error) {
	values, err := listRecords[api.UserMessage](ctx, u.tx, kindUserMessage, selector.RunID)
	if err != nil {
		return nil, err
	}
	filtered := values[:0]
	for _, value := range values {
		statuses := selector.Statuses
		if len(statuses) == 0 {
			statuses = []string{string(api.UserMessageQueued)}
		}
		if !contains(statuses, string(value.Status)) || !within(value.CreatedAt, selector.Since, selector.Until) {
			continue
		}
		filtered = append(filtered, value)
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].CreatedAt.Before(filtered[j].CreatedAt) })
	return limit(filtered, selector.Limit), nil
}

func (u *unitOfWork) ListQueuedMessages(ctx context.Context) ([]api.UserMessage, error) {
	return u.ListPendingFor(ctx, api.UserMessageSelector{})
}

func (u *unitOfWork) QueueEnvelope(ctx context.Context, value api.TaskEnvelope) error {
	return u.save(ctx, kindEnvelope, value.ID, "", value.RunID, value.TaskID, value.Status, value.CreatedAt, "", "", value, false)
}

func (u *unitOfWork) LoadEnvelope(ctx context.Context, id string) (api.TaskEnvelope, error) {
	return loadRecord[api.TaskEnvelope](ctx, u.tx, kindEnvelope, id, "")
}

func (u *unitOfWork) UpdateEnvelope(ctx context.Context, value api.TaskEnvelope) error {
	return u.save(ctx, kindEnvelope, value.ID, "", value.RunID, value.TaskID, value.Status, value.CreatedAt, "", "", value, true)
}

func (u *unitOfWork) ListEnvelopes(ctx context.Context, runID string) ([]api.TaskEnvelope, error) {
	return listRecords[api.TaskEnvelope](ctx, u.tx, kindEnvelope, runID)
}

func (u *unitOfWork) SaveApproval(ctx context.Context, value api.ApprovalRequest) error {
	return u.save(ctx, kindApproval, value.ApprovalID, "", value.RunID, value.TaskID, value.Status, time.Time{}, "", "", value, true)
}

func (u *unitOfWork) LoadApproval(ctx context.Context, id string) (api.ApprovalRequest, error) {
	return loadRecord[api.ApprovalRequest](ctx, u.tx, kindApproval, id, "")
}

func (u *unitOfWork) SaveResumeToken(ctx context.Context, value api.ResumeToken) error {
	status := value.Metadata["status"]
	if status == "" {
		status = "pending"
	}
	return u.save(ctx, kindResume, value.TokenID, "", value.RunID, value.TaskID, status, time.Time{}, "", "", value, true)
}

func (u *unitOfWork) LoadResumeToken(ctx context.Context, id string) (api.ResumeToken, error) {
	return loadRecord[api.ResumeToken](ctx, u.tx, kindResume, id, "")
}

func (u *unitOfWork) ListPending(ctx context.Context, selector api.ResumeTokenSelector) ([]api.ResumeToken, error) {
	values, err := listRecords[api.ResumeToken](ctx, u.tx, kindResume, selector.RunID)
	if err != nil {
		return nil, err
	}
	filtered := values[:0]
	now := time.Now()
	for _, value := range values {
		status := value.Metadata["status"]
		if status == "" {
			status = "pending"
		}
		if value.TaskID != selector.TaskID && selector.TaskID != "" || len(selector.Statuses) > 0 && !contains(selector.Statuses, status) || status == "consumed" || !value.ExpiresAt.IsZero() && value.ExpiresAt.Before(now) {
			continue
		}
		filtered = append(filtered, value)
	}
	if selector.Cursor != "" {
		index := 0
		for index < len(filtered) && filtered[index].TokenID <= selector.Cursor {
			index++
		}
		filtered = filtered[index:]
	}
	return limit(filtered, selector.Limit), nil
}

func (u *unitOfWork) save(ctx context.Context, kind, key1, key2, runID, taskID, status string, createdAt time.Time, toolName, idempotencyKey string, value any, upsert bool) error {
	data, err := marshalJSON(value)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", kind, err)
	}
	queries := dbgen.New(u.tx)
	params := dbgen.InsertRecordParams{Kind: kind, Key1: key1, Key2: key2, RunID: runID, TaskID: taskID, Status: status, CreatedAt: nanos(createdAt), ToolName: toolName, IdempotencyKey: idempotencyKey, Data: data}
	if upsert {
		err = queries.UpsertRecord(ctx, dbgen.UpsertRecordParams(params))
	} else {
		err = queries.InsertRecord(ctx, params)
	}
	if err != nil {
		if !upsert && isConstraint(err) {
			return fmt.Errorf("save %s: %w: key %q already exists", kind, errors.Join(api.ErrIdempotencyConflict, err), key1)
		}
		return fmt.Errorf("save %s: %w", kind, err)
	}
	return nil
}

func loadRecord[T any](ctx context.Context, tx *sql.Tx, kind string, key1 string, key2 string) (T, error) {
	var zero T
	data, err := dbgen.New(tx).GetRecordData(ctx, dbgen.GetRecordDataParams{Kind: kind, Key1: key1, Key2: key2})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return zero, api.ErrNotFound
		}
		return zero, fmt.Errorf("load %s: %w", kind, err)
	}
	if err := json.Unmarshal(data, &zero); err != nil {
		return zero, fmt.Errorf("decode %s: %w", kind, err)
	}
	return zero, nil
}

func listRecords[T any](ctx context.Context, tx *sql.Tx, kind string, runID string) ([]T, error) {
	queries := dbgen.New(tx)
	var data [][]byte
	var err error
	if runID != "" {
		data, err = queries.ListRecordDataByRun(ctx, dbgen.ListRecordDataByRunParams{Kind: kind, RunID: runID})
	} else {
		data, err = queries.ListRecordData(ctx, kind)
	}
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", kind, err)
	}
	return decodeRows[T](data)
}

func decodeRows[T any](rows [][]byte) ([]T, error) {
	var values []T
	for _, data := range rows {
		var value T
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}
func marshalJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func nanos(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixNano()
}

func int64FromUint64(value uint64) (int64, error) {
	if value > math.MaxInt64 {
		return 0, fmt.Errorf("value %d overflows SQLite INTEGER", value)
	}
	return int64(value), nil
}

func uint64FromInt64(value int64) (uint64, error) {
	if value < 0 {
		return 0, fmt.Errorf("negative SQLite INTEGER %d cannot be converted to uint64", value)
	}
	return uint64(value), nil
}

func intFromInt64(value int64) (int, error) {
	converted := int(value)
	if int64(converted) != value {
		return 0, fmt.Errorf("value %d overflows int", value)
	}
	return converted, nil
}

func within(value, since, until time.Time) bool {
	return (since.IsZero() || !value.Before(since)) && (until.IsZero() || !value.After(until))
}

func contains[T comparable](values []T, target T) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func limit[T any](values []T, count int) []T {
	if count > 0 && len(values) > count {
		return values[:count]
	}
	return values
}

var _ api.UnitOfWork = (*unitOfWork)(nil)
var _ api.UserMessageOutboxScanner = (*unitOfWork)(nil)
