package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Viking602/venat/durable"
)

const durableStorageEnvelopeVersion = 1

type durableExecutionEnvelope struct {
	Version   int               `json:"version"`
	Execution durable.Execution `json:"execution"`
}

type durableAttemptEnvelope struct {
	Version int             `json:"version"`
	Attempt durable.Attempt `json:"attempt"`
}

type durableReceiptEnvelope struct {
	Version  int             `json:"version"`
	Response json.RawMessage `json:"response"`
}

func encodeStoredExecution(execution durable.Execution) ([]byte, error) {
	return json.Marshal(durableExecutionEnvelope{Version: durableStorageEnvelopeVersion, Execution: execution})
}

func decodeStoredExecution(payload []byte) (durable.Execution, error) {
	var envelope durableExecutionEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return durable.Execution{}, err
	}
	if envelope.Version != durableStorageEnvelopeVersion {
		return durable.Execution{}, fmt.Errorf("unsupported storage envelope version %d", envelope.Version)
	}
	return envelope.Execution, nil
}

func encodeStoredAttempt(attempt durable.Attempt) ([]byte, error) {
	return json.Marshal(durableAttemptEnvelope{Version: durableStorageEnvelopeVersion, Attempt: attempt})
}

func decodeStoredAttempt(payload []byte) (durable.Attempt, error) {
	var envelope durableAttemptEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return durable.Attempt{}, err
	}
	if envelope.Version != durableStorageEnvelopeVersion {
		return durable.Attempt{}, fmt.Errorf("unsupported storage envelope version %d", envelope.Version)
	}
	return envelope.Attempt, nil
}

func encodeStoredReceipt(response json.RawMessage) ([]byte, error) {
	return json.Marshal(durableReceiptEnvelope{
		Version:  durableStorageEnvelopeVersion,
		Response: append(json.RawMessage(nil), response...),
	})
}

func decodeStoredReceipt(payload []byte) (json.RawMessage, error) {
	var envelope durableReceiptEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, err
	}
	if envelope.Version != durableStorageEnvelopeVersion {
		return nil, fmt.Errorf("unsupported storage envelope version %d", envelope.Version)
	}
	if len(envelope.Response) == 0 || !json.Valid(envelope.Response) {
		return nil, fmt.Errorf("invalid durable receipt response")
	}
	return append(json.RawMessage(nil), envelope.Response...), nil
}

var _ durable.Backend = (*DurableBackend)(nil)

// DurableBackend stores Venat execution semantics in the Provider database.
// Every mutation runs under SQLite's immediate transaction lock and uses the
// database clock for lease decisions.
type DurableBackend struct {
	provider *Provider
}

// DurableBackend returns a durable execution backend sharing this Provider's
// connection pool and content-addressed blob store.
func (p *Provider) DurableBackend() durable.Backend {
	return &DurableBackend{provider: p}
}

type durableRecord struct {
	execution       durable.Execution
	executionDigest string
	nextToken       uint64
	attempts        map[string][]durable.Attempt
	attemptDigests  map[durableAttemptID]string
	receipts        map[receiptID]receiptRecord
	receiptDigests  map[receiptID]string
}

type durableAttemptID struct {
	operationID string
	number      int
}

type receiptID struct {
	kind string
	key  string
}

type receiptRecord struct {
	requestHash [32]byte
	leaseToken  uint64
	response    json.RawMessage
}

type durableTransaction struct {
	backend *DurableBackend
	unit    *unitOfWork
}

func (backend *DurableBackend) begin(ctx context.Context) (*durableTransaction, error) {
	if backend == nil || backend.provider == nil || backend.provider.db == nil {
		return nil, errors.New("sqlite durable backend is closed")
	}
	tx, err := backend.provider.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("begin durable transaction: %w", err)
	}
	return &durableTransaction{
		backend: backend,
		unit: &unitOfWork{
			db:    backend.provider.db,
			tx:    tx,
			blobs: backend.provider.Blobs(),
		},
	}, nil
}

func (transaction *durableTransaction) rollback() {
	if transaction != nil && transaction.unit != nil && !transaction.unit.closed {
		_ = transaction.unit.Rollback(context.Background())
	}
}

func (transaction *durableTransaction) now(ctx context.Context) (time.Time, error) {
	var nanoseconds int64
	if err := transaction.unit.tx.QueryRowContext(ctx, `SELECT CAST((julianday('now') - 2440587.5) * 86400000000000 AS INTEGER)`).Scan(&nanoseconds); err != nil {
		return time.Time{}, fmt.Errorf("read SQLite trusted time: %w", err)
	}
	return time.Unix(0, nanoseconds).UTC(), nil
}

func (transaction *durableTransaction) load(ctx context.Context, executionID durable.ExecutionID) (*durableRecord, error) {
	var (
		storedID       string
		specHash       []byte
		status         string
		version        uint64
		leaseOwner     string
		leaseClaim     []byte
		leaseToken     uint64
		leaseExpiresAt int64
		nextToken      uint64
		inline         []byte
		digest         string
	)
	err := transaction.unit.tx.QueryRowContext(ctx, `SELECT execution_id,spec_hash,status,version,lease_owner,lease_claim,lease_token,lease_expires_at,next_lease_token,execution_inline,execution_digest FROM agent_executions WHERE execution_id=?`, executionID).Scan(
		&storedID, &specHash, &status, &version, &leaseOwner, &leaseClaim, &leaseToken, &leaseExpiresAt, &nextToken, &inline, &digest,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, executionError(executionID, durable.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("load durable execution %q: %w", executionID, err)
	}
	payload, err := loadPayload(ctx, transaction.backend.provider.Blobs(), inline, digest)
	if err != nil {
		return nil, fmt.Errorf("load durable execution %q payload: %w", executionID, err)
	}
	execution, err := decodeStoredExecution(payload)
	if err != nil {
		return nil, fmt.Errorf("decode durable execution %q: %w", executionID, err)
	}
	if err := validateStoredExecution(execution, storedID, specHash, status, version, leaseOwner, leaseClaim, leaseToken, leaseExpiresAt); err != nil {
		return nil, err
	}
	if err := validateExecutionHashes(execution); err != nil {
		return nil, executionError(executionID, err)
	}
	record := &durableRecord{
		execution:       execution,
		executionDigest: digest,
		nextToken:       nextToken,
		attempts:        make(map[string][]durable.Attempt),
		attemptDigests:  make(map[durableAttemptID]string),
		receipts:        make(map[receiptID]receiptRecord),
		receiptDigests:  make(map[receiptID]string),
	}
	if record.nextToken == 0 || (record.execution.Lease != nil && record.execution.Lease.Token > record.nextToken) {
		return nil, fmt.Errorf("durable execution %q has invalid lease token fence", executionID)
	}
	if err := transaction.loadAttempts(ctx, executionID, record); err != nil {
		return nil, err
	}
	if err := transaction.loadReceipts(ctx, executionID, record); err != nil {
		return nil, err
	}
	if err := validateDurableRecord(record); err != nil {
		return nil, fmt.Errorf("validate durable execution %q after load: %w", executionID, err)
	}
	return record, nil
}

func (transaction *durableTransaction) loadAttempts(ctx context.Context, executionID durable.ExecutionID, record *durableRecord) error {
	rows, err := transaction.unit.tx.QueryContext(ctx, `SELECT operation_id,attempt_number,kind,input_hash,status,lease_owner,lease_token,version,attempt_inline,attempt_digest FROM agent_effect_attempts WHERE execution_id=? ORDER BY operation_id,attempt_number`, executionID)
	if err != nil {
		return fmt.Errorf("list durable attempts for %q: %w", executionID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			operationID string
			number      int
			kind        string
			inputHash   []byte
			status      string
			leaseOwner  string
			leaseToken  uint64
			version     uint64
			inline      []byte
			digest      string
		)
		if err := rows.Scan(&operationID, &number, &kind, &inputHash, &status, &leaseOwner, &leaseToken, &version, &inline, &digest); err != nil {
			return fmt.Errorf("scan durable attempt for %q: %w", executionID, err)
		}
		payload, err := loadPayload(ctx, transaction.backend.provider.Blobs(), inline, digest)
		if err != nil {
			return fmt.Errorf("load durable attempt %q/%q/%d: %w", executionID, operationID, number, err)
		}
		attempt, err := decodeStoredAttempt(payload)
		if err != nil {
			return fmt.Errorf("decode durable attempt %q/%q/%d: %w", executionID, operationID, number, err)
		}
		if err := validateStoredAttempt(attempt, executionID, operationID, number, kind, inputHash, status, leaseOwner, leaseToken, version); err != nil {
			return err
		}
		if err := validateAttemptPayload(attempt, record.nextToken); err != nil {
			return fmt.Errorf("validate durable attempt %q/%q/%d: %w", executionID, operationID, number, err)
		}
		if want := len(record.attempts[operationID]) + 1; attempt.Number != want {
			return fmt.Errorf("durable attempt %q/%q number %d is not contiguous from 1", executionID, operationID, attempt.Number)
		}
		record.attemptDigests[durableAttemptID{operationID: operationID, number: attempt.Number}] = digest
		record.attempts[operationID] = append(record.attempts[operationID], attempt)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate durable attempts for %q: %w", executionID, err)
	}
	return nil
}

func (transaction *durableTransaction) loadReceipts(ctx context.Context, executionID durable.ExecutionID, record *durableRecord) error {
	rows, err := transaction.unit.tx.QueryContext(ctx, `SELECT command_kind,command_key,request_hash,lease_token,receipt_inline,receipt_digest FROM agent_execution_receipts WHERE execution_id=?`, executionID)
	if err != nil {
		return fmt.Errorf("list durable receipts for %q: %w", executionID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			kind        string
			key         string
			requestHash []byte
			leaseToken  uint64
			inline      []byte
			digest      string
		)
		if err := rows.Scan(&kind, &key, &requestHash, &leaseToken, &inline, &digest); err != nil {
			return fmt.Errorf("scan durable receipt for %q: %w", executionID, err)
		}
		if len(requestHash) != sha256.Size {
			return fmt.Errorf("durable receipt %q/%s/%s has invalid request hash", executionID, kind, key)
		}
		payload, err := loadPayload(ctx, transaction.backend.provider.Blobs(), inline, digest)
		if err != nil {
			return fmt.Errorf("load durable receipt %q/%s/%s: %w", executionID, kind, key, err)
		}
		response, err := decodeStoredReceipt(payload)
		if err != nil {
			return fmt.Errorf("decode durable receipt %q/%s/%s: %w", executionID, kind, key, err)
		}
		if kind == "" || key == "" || leaseToken == 0 || leaseToken > record.nextToken {
			return fmt.Errorf("durable receipt %q/%s/%s has invalid identity or fence", executionID, kind, key)
		}
		var hash [sha256.Size]byte
		copy(hash[:], requestHash)
		record.receipts[receiptID{kind: kind, key: key}] = receiptRecord{
			requestHash: hash,
			leaseToken:  leaseToken,
			response:    response,
		}
		record.receiptDigests[receiptID{kind: kind, key: key}] = digest
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate durable receipts for %q: %w", executionID, err)
	}
	return nil
}

func (transaction *durableTransaction) save(ctx context.Context, record *durableRecord, now time.Time) error {
	if err := validateDurableRecord(record); err != nil {
		return fmt.Errorf("validate durable execution %q before save: %w", record.execution.ID, err)
	}
	payload, err := encodeStoredExecution(record.execution)
	if err != nil {
		return fmt.Errorf("encode durable execution %q: %w", record.execution.ID, err)
	}
	inline, digest := preparePayload(payload)
	if record.executionDigest != "" && record.executionDigest != digest {
		transaction.unit.stageObsoletePayload(record.executionDigest)
	}
	record.executionDigest = digest
	transaction.unit.stagePayload(digest, payload)
	leaseOwner, leaseClaim, leaseToken, leaseExpiresAt := storedLease(record.execution.Lease)
	if _, err := transaction.unit.tx.ExecContext(ctx, `INSERT INTO agent_executions(execution_id,spec_hash,status,version,lease_owner,lease_claim,lease_token,lease_expires_at,next_lease_token,execution_inline,execution_digest,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(execution_id) DO UPDATE SET spec_hash=excluded.spec_hash,status=excluded.status,version=excluded.version,lease_owner=excluded.lease_owner,lease_claim=excluded.lease_claim,lease_token=excluded.lease_token,lease_expires_at=excluded.lease_expires_at,next_lease_token=excluded.next_lease_token,execution_inline=excluded.execution_inline,execution_digest=excluded.execution_digest,updated_at=excluded.updated_at`,
		record.execution.ID, record.execution.SpecHash[:], record.execution.Status, record.execution.Version, leaseOwner, leaseClaim, leaseToken, leaseExpiresAt, record.nextToken, inline, digest, now.UnixNano(),
	); err != nil {
		return fmt.Errorf("save durable execution %q: %w", record.execution.ID, err)
	}
	if err := transaction.saveAttempts(ctx, record); err != nil {
		return err
	}
	return transaction.saveReceipts(ctx, record)
}

func (transaction *durableTransaction) saveAttempts(ctx context.Context, record *durableRecord) error {
	operationIDs := make([]string, 0, len(record.attempts))
	for operationID := range record.attempts {
		operationIDs = append(operationIDs, operationID)
	}
	sort.Strings(operationIDs)
	for _, operationID := range operationIDs {
		for _, attempt := range record.attempts[operationID] {
			payload, err := encodeStoredAttempt(attempt)
			if err != nil {
				return fmt.Errorf("encode durable attempt %q/%q/%d: %w", record.execution.ID, operationID, attempt.Number, err)
			}
			inline, digest := preparePayload(payload)
			transaction.unit.stagePayload(digest, payload)
			id := durableAttemptID{operationID: operationID, number: attempt.Number}
			if prior := record.attemptDigests[id]; prior != "" && prior != digest {
				transaction.unit.stageObsoletePayload(prior)
			}
			record.attemptDigests[id] = digest
			leaseOwner, leaseToken := storedLeaseRef(attempt.Lease)
			if _, err := transaction.unit.tx.ExecContext(ctx, `INSERT INTO agent_effect_attempts(execution_id,operation_id,attempt_number,kind,input_hash,status,lease_owner,lease_token,version,attempt_inline,attempt_digest) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(execution_id,operation_id,attempt_number) DO UPDATE SET kind=excluded.kind,input_hash=excluded.input_hash,status=excluded.status,lease_owner=excluded.lease_owner,lease_token=excluded.lease_token,version=excluded.version,attempt_inline=excluded.attempt_inline,attempt_digest=excluded.attempt_digest`,
				record.execution.ID, operationID, attempt.Number, attempt.Kind, attempt.InputHash[:], attempt.Status, leaseOwner, leaseToken, attempt.Version, inline, digest,
			); err != nil {
				return fmt.Errorf("save durable attempt %q/%q/%d: %w", record.execution.ID, operationID, attempt.Number, err)
			}
		}
	}
	return nil
}

func (transaction *durableTransaction) saveReceipts(ctx context.Context, record *durableRecord) error {
	ids := make([]receiptID, 0, len(record.receipts))
	for id := range record.receipts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool {
		if ids[left].kind != ids[right].kind {
			return ids[left].kind < ids[right].kind
		}
		return ids[left].key < ids[right].key
	})
	for _, id := range ids {
		receipt := record.receipts[id]
		payload, err := encodeStoredReceipt(receipt.response)
		if err != nil {
			return fmt.Errorf("encode durable receipt %q/%s/%s: %w", record.execution.ID, id.kind, id.key, err)
		}
		inline, digest := preparePayload(payload)
		transaction.unit.stagePayload(digest, payload)
		if prior := record.receiptDigests[id]; prior != "" && prior != digest {
			transaction.unit.stageObsoletePayload(prior)
		}
		if _, err := transaction.unit.tx.ExecContext(ctx, `INSERT INTO agent_execution_receipts(execution_id,command_kind,command_key,request_hash,lease_token,receipt_inline,receipt_digest) VALUES(?,?,?,?,?,?,?) ON CONFLICT(execution_id,command_kind,command_key) DO UPDATE SET request_hash=excluded.request_hash,lease_token=excluded.lease_token,receipt_inline=excluded.receipt_inline,receipt_digest=excluded.receipt_digest`,
			record.execution.ID, id.kind, id.key, receipt.requestHash[:], receipt.leaseToken, inline, digest,
		); err != nil {
			return fmt.Errorf("save durable receipt %q/%s/%s: %w", record.execution.ID, id.kind, id.key, err)
		}
		record.receiptDigests[id] = digest
	}
	return nil
}

func (backend *DurableBackend) withRecord(ctx context.Context, executionID durable.ExecutionID, operationID string, mutate func(*durableRecord, time.Time) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	transaction, err := backend.begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.rollback()
	record, err := transaction.load(ctx, executionID)
	if err != nil {
		if operationID != "" && errors.Is(err, durable.ErrNotFound) {
			return attemptError(executionID, operationID, 0, durable.ErrNotFound)
		}
		return err
	}
	now, err := transaction.now(ctx)
	if err != nil {
		return err
	}
	if err := mutate(record, now); err != nil {
		return err
	}
	if err := transaction.save(ctx, record, now); err != nil {
		return err
	}
	return transaction.unit.Commit(ctx)
}

func (backend *DurableBackend) LoadExecution(ctx context.Context, executionID durable.ExecutionID) (durable.Execution, error) {
	if !validExecutionID(executionID) {
		return durable.Execution{}, contextOrValidation(ctx, executionError(executionID, durable.ErrInvalidArgument))
	}
	transaction, err := backend.begin(ctx)
	if err != nil {
		return durable.Execution{}, err
	}
	defer transaction.rollback()
	record, err := transaction.load(ctx, executionID)
	if err != nil {
		return durable.Execution{}, err
	}
	if err := transaction.unit.Commit(ctx); err != nil {
		return durable.Execution{}, err
	}
	return cloneJSON(record.execution), nil
}

func requestHash(value any) ([32]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(payload), nil
}

func putReceipt[T any](record *durableRecord, id receiptID, hash [32]byte, token uint64, response T) error {
	payload, err := json.Marshal(response)
	if err != nil {
		return err
	}
	record.receipts[id] = receiptRecord{requestHash: hash, leaseToken: token, response: payload}
	return nil
}

func getReceipt[T any](record *durableRecord, id receiptID, hash [32]byte) (T, bool, error) {
	var value T
	receipt, ok := record.receipts[id]
	if !ok {
		return value, false, nil
	}
	if receipt.requestHash != hash {
		return value, true, durable.ErrConflict
	}
	if err := json.Unmarshal(receipt.response, &value); err != nil {
		return value, true, fmt.Errorf("decode durable receipt %s/%s: %w", id.kind, id.key, err)
	}
	return value, true, nil
}

func claimReceiptID(mode string, claim durable.ClaimID) receiptID {
	return receiptID{kind: mode, key: hex.EncodeToString(claim[:])}
}

func leaseReceiptID(kind string, lease durable.LeaseRef, suffix string) receiptID {
	sum := sha256.Sum256([]byte(lease.OwnerID))
	return receiptID{kind: kind, key: fmt.Sprintf("%x:%d:%s", sum[:8], lease.Token, suffix)}
}

func storedLease(lease *durable.Lease) (owner string, claim []byte, token uint64, expiresAt int64) {
	if lease == nil {
		return "", []byte{}, 0, 0
	}
	return lease.OwnerID, append([]byte(nil), lease.ClaimID[:]...), lease.Token, lease.ExpiresAt.UnixNano()
}

func storedLeaseRef(lease *durable.LeaseRef) (owner string, token uint64) {
	if lease == nil {
		return "", 0
	}
	return lease.OwnerID, lease.Token
}

func validateStoredExecution(execution durable.Execution, storedID string, specHash []byte, status string, version uint64, leaseOwner string, leaseClaim []byte, leaseToken uint64, leaseExpiresAt int64) error {
	if string(execution.ID) != storedID || len(specHash) != sha256.Size || !bytes.Equal(execution.SpecHash[:], specHash) || string(execution.Status) != status || execution.Version != version {
		return fmt.Errorf("durable execution %q catalog disagrees with payload", storedID)
	}
	owner, claim, token, expiresAt := storedLease(execution.Lease)
	if owner != leaseOwner || !bytes.Equal(claim, leaseClaim) || token != leaseToken || expiresAt != leaseExpiresAt {
		return fmt.Errorf("durable execution %q lease catalog disagrees with payload", storedID)
	}
	return nil
}

func validateExecutionHashes(execution durable.Execution) error {
	if !validExecutionID(execution.ID) || execution.Version == 0 {
		return fmt.Errorf("invalid durable execution identity or version")
	}
	specHash, err := durable.HashExecutionSpec(execution.Spec)
	if err != nil {
		return fmt.Errorf("hash execution spec: %w", err)
	}
	if specHash != execution.SpecHash {
		return fmt.Errorf("execution spec hash mismatch")
	}
	if execution.Checkpoint != nil {
		if err := durable.ValidateCheckpoint(*execution.Checkpoint); err != nil {
			return err
		}
	}
	switch execution.Status {
	case durable.ExecutionStatusRunning, durable.ExecutionStatusSuspended:
		if execution.Result != nil || !zeroHash(execution.ResultHash) {
			return fmt.Errorf("non-terminal execution contains a result")
		}
		if execution.Status == durable.ExecutionStatusSuspended && execution.Lease != nil {
			return fmt.Errorf("suspended execution contains a lease")
		}
	case durable.ExecutionStatusCompleted, durable.ExecutionStatusFailed:
		if execution.Result == nil || execution.Lease != nil {
			return fmt.Errorf("terminal execution has invalid result or lease")
		}
		resultHash, err := durable.HashResult(*execution.Result)
		if err != nil {
			return fmt.Errorf("hash execution result: %w", err)
		}
		if resultHash != execution.ResultHash {
			return fmt.Errorf("execution result hash mismatch")
		}
		if (execution.Status == durable.ExecutionStatusCompleted) != (execution.Result.Failure == nil) {
			return fmt.Errorf("execution status disagrees with result failure")
		}
	default:
		return fmt.Errorf("invalid durable execution status %q", execution.Status)
	}
	if execution.Lease != nil && (strings.TrimSpace(execution.Lease.OwnerID) == "" ||
		execution.Lease.ClaimID == (durable.ClaimID{}) || execution.Lease.Token == 0 ||
		execution.Lease.ExpiresAt.IsZero()) {
		return fmt.Errorf("invalid durable execution lease")
	}
	return nil
}

func validateAttemptPayload(attempt durable.Attempt, nextToken uint64) error {
	if attempt.Version == 0 || zeroHash(attempt.InputHash) {
		return fmt.Errorf("invalid attempt version or input hash")
	}
	if attempt.Kind != durable.AttemptKindModel && attempt.Kind != durable.AttemptKindTool {
		return fmt.Errorf("invalid attempt kind %q", attempt.Kind)
	}
	if attempt.Lease != nil && (strings.TrimSpace(attempt.Lease.OwnerID) == "" ||
		attempt.Lease.Token == 0 || attempt.Lease.Token > nextToken) {
		return fmt.Errorf("invalid attempt lease")
	}
	switch attempt.Status {
	case durable.AttemptStatusRunning:
		if attempt.Lease == nil || len(attempt.Payload) != 0 || attempt.Failure != nil {
			return fmt.Errorf("running attempt has invalid settlement")
		}
	case durable.AttemptStatusSucceeded:
		if attempt.Lease != nil || attempt.Failure != nil {
			return fmt.Errorf("succeeded attempt has invalid settlement")
		}
	case durable.AttemptStatusFailed:
		if attempt.Lease != nil || attempt.Failure == nil {
			return fmt.Errorf("failed attempt has invalid settlement")
		}
	case durable.AttemptStatusUnknown:
		if attempt.Lease != nil {
			return fmt.Errorf("unknown attempt retains a lease")
		}
	case durable.AttemptStatusAbandoned:
		if attempt.Lease != nil || len(attempt.Payload) != 0 || attempt.Failure != nil {
			return fmt.Errorf("abandoned attempt has invalid settlement")
		}
	default:
		return fmt.Errorf("invalid attempt status %q", attempt.Status)
	}
	return nil
}

func validateDurableRecord(record *durableRecord) error {
	if record == nil || record.nextToken == 0 ||
		(record.execution.Lease != nil && record.execution.Lease.Token > record.nextToken) {
		return fmt.Errorf("invalid durable execution token fence")
	}
	if err := validateExecutionHashes(record.execution); err != nil {
		return err
	}
	for operationID, attempts := range record.attempts {
		if strings.TrimSpace(operationID) == "" {
			return fmt.Errorf("empty durable operation identity")
		}
		for index, attempt := range attempts {
			if attempt.ExecutionID != record.execution.ID || attempt.OperationID != operationID ||
				attempt.Number != index+1 {
				return fmt.Errorf("attempt identity disagrees with durable record")
			}
			if err := validateAttemptPayload(attempt, record.nextToken); err != nil {
				return err
			}
		}
	}
	for id, receipt := range record.receipts {
		if id.kind == "" || id.key == "" || receipt.requestHash == ([sha256.Size]byte{}) ||
			receipt.leaseToken == 0 || receipt.leaseToken > record.nextToken ||
			len(receipt.response) == 0 || !json.Valid(receipt.response) {
			return fmt.Errorf("invalid durable receipt")
		}
	}
	return nil
}

func validateStoredAttempt(attempt durable.Attempt, executionID durable.ExecutionID, operationID string, number int, kind string, inputHash []byte, status string, leaseOwner string, leaseToken uint64, version uint64) error {
	owner, token := storedLeaseRef(attempt.Lease)
	if attempt.ExecutionID != executionID || attempt.OperationID != operationID || attempt.Number != number || string(attempt.Kind) != kind || len(inputHash) != sha256.Size || !bytes.Equal(attempt.InputHash[:], inputHash) || string(attempt.Status) != status || owner != leaseOwner || token != leaseToken || attempt.Version != version {
		return fmt.Errorf("durable attempt %q/%q/%d catalog disagrees with payload", executionID, operationID, number)
	}
	return nil
}

func executionError(executionID durable.ExecutionID, err error) error {
	return &durable.ExecutionError{ExecutionID: executionID, Err: err}
}

func attemptError(executionID durable.ExecutionID, operationID string, attemptNumber int, err error) error {
	return &durable.AttemptError{ExecutionID: executionID, OperationID: operationID, AttemptNumber: attemptNumber, Err: err}
}

func validExecutionID(executionID durable.ExecutionID) bool {
	return strings.TrimSpace(string(executionID)) != ""
}

func validLeaseInput(ownerID string, claimID durable.ClaimID, ttl time.Duration) bool {
	return strings.TrimSpace(ownerID) != "" && claimID != (durable.ClaimID{}) && ttl > 0
}

func leaseMatches(lease *durable.Lease, reference durable.LeaseRef) bool {
	return lease != nil && lease.OwnerID == reference.OwnerID && lease.Token == reference.Token
}

func leaseActive(lease *durable.Lease, now time.Time) bool {
	return lease != nil && lease.ExpiresAt.After(now)
}

func requireActiveLease(record *durableRecord, executionID durable.ExecutionID, reference durable.LeaseRef, now time.Time) error {
	if !leaseMatches(record.execution.Lease, reference) || !leaseActive(record.execution.Lease, now) {
		return executionError(executionID, durable.ErrLeaseLost)
	}
	return nil
}

func terminal(status durable.ExecutionStatus) bool {
	return status == durable.ExecutionStatusCompleted || status == durable.ExecutionStatusFailed
}

func contextOrValidation(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func cloneJSON[T any](value T) T {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var cloned T
	if err := json.Unmarshal(payload, &cloned); err != nil {
		panic(err)
	}
	return cloned
}

func sameFailure(left, right *durable.FailureRecord) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func zeroHash(hash [32]byte) bool {
	return hash == ([32]byte{})
}
