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

	"github.com/Viking602/azem/internal/blobstore"
	"github.com/Viking602/azem/internal/store/sqlite/dbgen"
	"github.com/Viking602/venat/api"
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
	db      *sql.DB
	tx      *sql.Tx
	blobs   blobstore.Store
	pending map[string][]byte
	closed  bool
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
	installed, err := u.installPending(ctx)
	u.pending = nil
	if err != nil {
		_ = u.tx.Rollback()
		return errors.Join(err, u.cleanupInstalled(installed))
	}
	if err := u.tx.Commit(); err != nil {
		return errors.Join(err, u.cleanupInstalled(installed))
	}
	return nil
}

func (u *unitOfWork) installPending(ctx context.Context) ([]string, error) {
	digests := make([]string, 0, len(u.pending))
	for digest := range u.pending {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	installed := make([]string, 0, len(digests))
	for _, digest := range digests {
		created, err := u.installPendingPayload(ctx, digest)
		if err != nil {
			return installed, err
		}
		if created {
			installed = append(installed, digest)
		}
	}
	return installed, nil
}

func (u *unitOfWork) installPendingPayload(ctx context.Context, digest string) (bool, error) {
	referenced, err := payloadReferenced(ctx, u.tx, digest)
	if err != nil {
		return false, fmt.Errorf("resolve blob payload %s: %w", digest, err)
	}
	if !referenced {
		return false, nil
	}
	created, err := u.blobs.InstallAt(ctx, digest, u.pending[digest])
	if err != nil {
		return false, fmt.Errorf("commit blob payload %s: %w", digest, err)
	}
	return created, nil
}

func (u *unitOfWork) cleanupInstalled(digests []string) error {
	if len(digests) == 0 || u.db == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := beginImmediate(ctx, u.db)
	if err != nil {
		return err
	}
	defer conn.Close()
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	cleanupErr := u.cleanupInstalledDigests(ctx, conn, digests)
	commitErr := commitImmediate(ctx, conn)
	return errors.Join(cleanupErr, commitErr)
}

func beginImmediate(ctx context.Context, db *sql.DB) (*sql.Conn, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("open orphaned blob cleanup connection: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("begin immediate orphaned blob cleanup: %w", err)
	}
	return conn, nil
}

func commitImmediate(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit orphaned blob cleanup: %w", err)
	}
	return nil
}

func (u *unitOfWork) cleanupInstalledDigests(ctx context.Context, queryer rowQueryer, digests []string) error {
	var cleanupErrors []error
	for _, digest := range digests {
		if err := u.cleanupInstalledDigest(ctx, queryer, digest); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func (u *unitOfWork) cleanupInstalledDigest(ctx context.Context, queryer rowQueryer, digest string) error {
	referenced, err := payloadReferenced(ctx, queryer, digest)
	if err != nil {
		return fmt.Errorf("check orphaned blob %s: %w", digest, err)
	}
	if referenced {
		return nil
	}
	return u.blobs.Delete(ctx, digest)
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func payloadReferenced(ctx context.Context, queryer rowQueryer, digest string) (bool, error) {
	var referenced bool
	err := queryer.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM events WHERE data_sha256 = ?
		UNION ALL
		SELECT 1 FROM records WHERE data_sha256 = ?
	)`, digest, digest).Scan(&referenced)
	return referenced, err
}

func (u *unitOfWork) Rollback(context.Context) error {
	if u.closed {
		return sql.ErrTxDone
	}
	u.closed = true
	u.pending = nil
	return u.tx.Rollback()
}

func (u *unitOfWork) stagePayload(digest string, payload []byte) {
	if digest == "" {
		return
	}
	if u.pending == nil {
		u.pending = make(map[string][]byte)
	}
	u.pending[digest] = append([]byte(nil), payload...)
}

func (u *unitOfWork) loadPayload(ctx context.Context, inline []byte, digest string) ([]byte, error) {
	if digest != "" {
		if payload, ok := u.pending[digest]; ok {
			return append([]byte(nil), payload...), nil
		}
	}
	return loadPayload(ctx, u.blobs, inline, digest)
}

func (u *unitOfWork) SaveRun(ctx context.Context, value api.Run) error {
	return u.save(ctx, kindRun, value.ID, "", value.ID, "", string(value.Status), value.CreatedAt, "", "", value, true)
}

func (u *unitOfWork) LoadRun(ctx context.Context, id string) (api.Run, error) {
	return loadRecord[api.Run](ctx, u, kindRun, id, "")
}

func (u *unitOfWork) ListRuns(ctx context.Context, selector api.RunSelector) ([]api.Run, error) {
	values, err := listRecords[api.Run](ctx, u, kindRun, "")
	if err != nil {
		return nil, err
	}
	filtered := values[:0]
	for _, value := range values {
		if len(selector.IDs) > 0 && !contains(selector.IDs, value.ID) || len(selector.Statuses) > 0 && !contains(selector.Statuses, value.Status) || !within(value.CreatedAt, selector.Since, selector.Until) {
			continue
		}
		agentVersion := value.AgentVersion
		if agentVersion == "" {
			agentVersion = value.Metadata["agent_version"]
		}
		if selector.AgentID != "" && value.Metadata["agent_id"] != selector.AgentID || selector.AgentVersion != "" && agentVersion != selector.AgentVersion {
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
	return loadRecord[api.Task](ctx, u, kindTask, taskID, runID)
}

func (u *unitOfWork) ListTasks(ctx context.Context, runID string) ([]api.Task, error) {
	return listRecords[api.Task](ctx, u, kindTask, runID)
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
	inline, digest := preparePayload(data)
	err = queries.InsertEvent(ctx, dbgen.InsertEventParams{RunID: value.RunID, Sequence: int64(value.Sequence), RecordedAt: nanos(value.RecordedAt), Data: inline, DataSha256: digest})
	if err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	u.stagePayload(digest, data)
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
	var payloads []payloadRow
	if strict {
		sequence, err := int64FromUint64(afterSeq)
		if err != nil {
			return nil, fmt.Errorf("list events: %w", err)
		}
		rows, err := queries.ListEventDataAfter(ctx, dbgen.ListEventDataAfterParams{RunID: runID, Sequence: sequence})
		if err != nil {
			return nil, fmt.Errorf("list events: %w", err)
		}
		for _, row := range rows {
			payloads = append(payloads, payloadRow{Data: row.Data, Digest: row.DataSha256})
		}
	} else {
		rows, err := queries.ListEventData(ctx, runID)
		if err != nil {
			return nil, fmt.Errorf("list events: %w", err)
		}
		for _, row := range rows {
			payloads = append(payloads, payloadRow{Data: row.Data, Digest: row.DataSha256})
		}
	}
	return decodePayloadRows[api.Event](ctx, u, payloads)
}

func (u *unitOfWork) SaveTraceSpan(ctx context.Context, value api.TraceSpan) error {
	return u.save(ctx, kindTrace, value.ID, "", value.RunID, value.TaskID, string(value.Status), value.StartedAt, "", "", value, true)
}

func (u *unitOfWork) ListTraceSpans(ctx context.Context, runID string) ([]api.TraceSpan, error) {
	return listRecords[api.TraceSpan](ctx, u, kindTrace, runID)
}

func (u *unitOfWork) WriteItem(ctx context.Context, value api.BlackboardItem) error {
	return u.save(ctx, kindBlackboard, value.ID, "", value.RunID, value.TaskID, string(value.Visibility), value.CreatedAt, "", "", value, true)
}

func (u *unitOfWork) SelectItems(ctx context.Context, runID string, selector api.BlackboardSelector) ([]api.BlackboardItem, error) {
	values, err := listRecords[api.BlackboardItem](ctx, u, kindBlackboard, runID)
	if err != nil {
		return nil, err
	}
	filtered := values[:0]
	for _, value := range values {
		if blackboardItemMatches(value, selector) {
			filtered = append(filtered, value)
		}
	}
	return limit(filtered, selector.Limit), nil
}

func blackboardItemMatches(value api.BlackboardItem, selector api.BlackboardSelector) bool {
	return blackboardOwnerMatches(value, selector) &&
		blackboardSourceMatches(value, selector) &&
		blackboardVersionMatches(value, selector)
}

func blackboardOwnerMatches(value api.BlackboardItem, selector api.BlackboardSelector) bool {
	return (selector.RunID == "" || value.RunID == selector.RunID) &&
		(selector.TaskID == "" || value.TaskID == selector.TaskID) &&
		(len(selector.ItemTypes) == 0 || contains(selector.ItemTypes, value.Type))
}

func blackboardSourceMatches(value api.BlackboardItem, selector api.BlackboardSelector) bool {
	return (len(selector.SourceTypes) == 0 || contains(selector.SourceTypes, value.Source.Type)) &&
		(len(selector.SourceIDs) == 0 || contains(selector.SourceIDs, value.Source.ID)) &&
		(len(selector.SourceAgentIDs) == 0 || contains(selector.SourceAgentIDs, value.Source.ID))
}

func blackboardVersionMatches(value api.BlackboardItem, selector api.BlackboardSelector) bool {
	return (selector.Visibility == "" || value.Visibility == selector.Visibility) &&
		(selector.SinceVersion == 0 || value.Version > selector.SinceVersion) &&
		(len(selector.Keys) == 0 || contains(selector.Keys, value.Key))
}

func (u *unitOfWork) QueueMessage(ctx context.Context, value api.UserMessage) error {
	return u.save(ctx, kindUserMessage, value.ID, value.RunID, value.RunID, value.TaskID, string(value.Status), value.CreatedAt, "", value.IdempotencyKey, value, false)
}

func (u *unitOfWork) LoadMessage(ctx context.Context, runID string, messageID string) (api.UserMessage, error) {
	return loadRecord[api.UserMessage](ctx, u, kindUserMessage, messageID, runID)
}

func (u *unitOfWork) UpdateMessage(ctx context.Context, value api.UserMessage) error {
	return u.save(ctx, kindUserMessage, value.ID, value.RunID, value.RunID, value.TaskID, string(value.Status), value.CreatedAt, "", value.IdempotencyKey, value, true)
}

func (u *unitOfWork) ListMessages(ctx context.Context, runID string) ([]api.UserMessage, error) {
	return listRecords[api.UserMessage](ctx, u, kindUserMessage, runID)
}

func (u *unitOfWork) ListPendingFor(ctx context.Context, selector api.UserMessageSelector) ([]api.UserMessage, error) {
	values, err := listRecords[api.UserMessage](ctx, u, kindUserMessage, selector.RunID)
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
	return loadRecord[api.TaskEnvelope](ctx, u, kindEnvelope, id, "")
}

func (u *unitOfWork) UpdateEnvelope(ctx context.Context, value api.TaskEnvelope) error {
	return u.save(ctx, kindEnvelope, value.ID, "", value.RunID, value.TaskID, value.Status, value.CreatedAt, "", "", value, true)
}

func (u *unitOfWork) ListEnvelopes(ctx context.Context, runID string) ([]api.TaskEnvelope, error) {
	return listRecords[api.TaskEnvelope](ctx, u, kindEnvelope, runID)
}

func (u *unitOfWork) SaveApproval(ctx context.Context, value api.ApprovalRequest) error {
	return u.save(ctx, kindApproval, value.ApprovalID, "", value.RunID, value.TaskID, value.Status, time.Time{}, "", "", value, true)
}

func (u *unitOfWork) LoadApproval(ctx context.Context, id string) (api.ApprovalRequest, error) {
	return loadRecord[api.ApprovalRequest](ctx, u, kindApproval, id, "")
}

func (u *unitOfWork) SaveResumeToken(ctx context.Context, value api.ResumeToken) error {
	status := value.Metadata["status"]
	if status == "" {
		status = "pending"
	}
	return u.save(ctx, kindResume, value.TokenID, "", value.RunID, value.TaskID, status, time.Time{}, "", "", value, true)
}

func (u *unitOfWork) LoadResumeToken(ctx context.Context, id string) (api.ResumeToken, error) {
	return loadRecord[api.ResumeToken](ctx, u, kindResume, id, "")
}

func (u *unitOfWork) ListPending(ctx context.Context, selector api.ResumeTokenSelector) ([]api.ResumeToken, error) {
	values, err := listRecords[api.ResumeToken](ctx, u, kindResume, selector.RunID)
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
	inline, digest := preparePayload(data)
	queries := dbgen.New(u.tx)
	params := dbgen.InsertRecordParams{Kind: kind, Key1: key1, Key2: key2, RunID: runID, TaskID: taskID, Status: status, CreatedAt: nanos(createdAt), ToolName: toolName, IdempotencyKey: idempotencyKey, Data: inline, DataSha256: digest}
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
	u.stagePayload(digest, data)
	return nil
}

func loadRecord[T any](ctx context.Context, u *unitOfWork, kind string, key1 string, key2 string) (T, error) {
	var zero T
	row, err := dbgen.New(u.tx).GetRecordData(ctx, dbgen.GetRecordDataParams{Kind: kind, Key1: key1, Key2: key2})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return zero, api.ErrNotFound
		}
		return zero, fmt.Errorf("load %s: %w", kind, err)
	}
	data, err := u.loadPayload(ctx, row.Data, row.DataSha256)
	if err != nil {
		return zero, fmt.Errorf("load %s: %w", kind, err)
	}
	if err := json.Unmarshal(data, &zero); err != nil {
		return zero, fmt.Errorf("decode %s: %w", kind, err)
	}
	return zero, nil
}

func listRecords[T any](ctx context.Context, u *unitOfWork, kind string, runID string) ([]T, error) {
	queries := dbgen.New(u.tx)
	var rows []payloadRow
	var err error
	if runID != "" {
		listed, listErr := queries.ListRecordDataByRun(ctx, dbgen.ListRecordDataByRunParams{Kind: kind, RunID: runID})
		if listErr != nil {
			err = listErr
		} else {
			for _, row := range listed {
				rows = append(rows, payloadRow{Data: row.Data, Digest: row.DataSha256})
			}
		}
	} else {
		listed, listErr := queries.ListRecordData(ctx, kind)
		if listErr != nil {
			err = listErr
		} else {
			for _, row := range listed {
				rows = append(rows, payloadRow{Data: row.Data, Digest: row.DataSha256})
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", kind, err)
	}
	return decodePayloadRows[T](ctx, u, rows)
}

type payloadRow struct {
	Data   []byte
	Digest string
}

func decodePayloadRows[T any](ctx context.Context, u *unitOfWork, rows []payloadRow) ([]T, error) {
	values := make([]T, 0, len(rows))
	for _, row := range rows {
		data, err := u.loadPayload(ctx, row.Data, row.Digest)
		if err != nil {
			return nil, err
		}
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

var (
	_ api.UnitOfWork               = (*unitOfWork)(nil)
	_ api.UserMessageOutboxScanner = (*unitOfWork)(nil)
)
