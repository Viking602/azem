package sqlite

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Viking602/venat/durable"
)

func (backend *DurableBackend) StartExecution(ctx context.Context, request durable.StartExecutionRequest) (durable.StartResult, error) {
	if err := ctx.Err(); err != nil {
		return durable.StartResult{}, err
	}
	if !validExecutionID(request.ExecutionID) || !validLeaseInput(request.OwnerID, request.ClaimID, request.LeaseTTL) {
		return durable.StartResult{}, executionError(request.ExecutionID, durable.ErrInvalidArgument)
	}
	hash, err := durable.HashExecutionSpec(request.Spec)
	if err != nil {
		return durable.StartResult{}, executionError(request.ExecutionID, fmt.Errorf("%w: hash execution spec: %v", durable.ErrInvalidArgument, err))
	}
	if hash != request.SpecHash {
		return durable.StartResult{}, executionError(request.ExecutionID, durable.ErrConflict)
	}
	requestDigest, err := requestHash(request)
	if err != nil {
		return durable.StartResult{}, executionError(request.ExecutionID, durable.ErrInvalidArgument)
	}

	transaction, err := backend.begin(ctx)
	if err != nil {
		return durable.StartResult{}, err
	}
	defer transaction.rollback()
	now, err := transaction.now(ctx)
	if err != nil {
		return durable.StartResult{}, err
	}
	record, loadErr := transaction.load(ctx, request.ExecutionID)
	if loadErr == nil {
		if record.execution.SpecHash != request.SpecHash {
			return durable.StartResult{}, executionError(request.ExecutionID, durable.ErrConflict)
		}
		if terminal(record.execution.Status) {
			if err := transaction.unit.Commit(ctx); err != nil {
				return durable.StartResult{}, err
			}
			return durable.StartResult{Execution: cloneJSON(record.execution)}, nil
		}
		result, err := claimForStart(record, request, requestDigest, now)
		if err != nil {
			return durable.StartResult{}, err
		}
		if err := transaction.save(ctx, record, now); err != nil {
			return durable.StartResult{}, err
		}
		if err := transaction.unit.Commit(ctx); err != nil {
			return durable.StartResult{}, err
		}
		return cloneJSON(result), nil
	}
	if !isDurableNotFound(loadErr) {
		return durable.StartResult{}, loadErr
	}
	record = &durableRecord{
		execution: durable.Execution{
			ID:       request.ExecutionID,
			Spec:     cloneJSON(request.Spec),
			SpecHash: request.SpecHash,
			Status:   durable.ExecutionStatusRunning,
			Version:  1,
		},
		nextToken:      1,
		attempts:       make(map[string][]durable.Attempt),
		attemptDigests: make(map[durableAttemptID]string),
		receipts:       make(map[receiptID]receiptRecord),
		receiptDigests: make(map[receiptID]string),
	}
	record.execution.Lease = &durable.Lease{
		OwnerID:   request.OwnerID,
		ClaimID:   request.ClaimID,
		Token:     record.nextToken,
		ExpiresAt: now.Add(request.LeaseTTL),
	}
	result := durable.StartResult{Execution: cloneJSON(record.execution), Created: true}
	if err := putReceipt(record, claimReceiptID("start", request.ClaimID), requestDigest, record.nextToken, result); err != nil {
		return durable.StartResult{}, err
	}
	if err := transaction.save(ctx, record, now); err != nil {
		return durable.StartResult{}, err
	}
	if err := transaction.unit.Commit(ctx); err != nil {
		return durable.StartResult{}, err
	}
	return cloneJSON(result), nil
}

func claimForStart(record *durableRecord, request durable.StartExecutionRequest, requestDigest [32]byte, now time.Time) (durable.StartResult, error) {
	id := claimReceiptID("start", request.ClaimID)
	if prior, ok := record.receipts[id]; ok {
		if record.nextToken > prior.leaseToken {
			return durable.StartResult{}, executionError(request.ExecutionID, durable.ErrLeaseLost)
		}
		result, _, err := getReceipt[durable.StartResult](record, id, requestDigest)
		if err != nil {
			return durable.StartResult{}, executionError(request.ExecutionID, err)
		}
		if leaseActive(record.execution.Lease, now) && record.execution.Lease.ClaimID == request.ClaimID {
			return cloneJSON(result), nil
		}
	}
	if leaseActive(record.execution.Lease, now) {
		return durable.StartResult{}, executionError(request.ExecutionID, durable.ErrBusy)
	}
	reconcile := reconcileForClaim(record)
	record.nextToken++
	record.execution.Status = durable.ExecutionStatusRunning
	record.execution.Lease = &durable.Lease{
		OwnerID:   request.OwnerID,
		ClaimID:   request.ClaimID,
		Token:     record.nextToken,
		ExpiresAt: now.Add(request.LeaseTTL),
	}
	result := durable.StartResult{Execution: cloneJSON(record.execution), Reconcile: reconcile}
	if err := putReceipt(record, id, requestDigest, record.nextToken, result); err != nil {
		return durable.StartResult{}, err
	}
	return cloneJSON(result), nil
}

func (backend *DurableBackend) ResumeExecution(ctx context.Context, request durable.ResumeExecutionRequest) (durable.ResumeResult, error) {
	if !validExecutionID(request.ExecutionID) || !validLeaseInput(request.OwnerID, request.ClaimID, request.LeaseTTL) {
		return durable.ResumeResult{}, contextOrValidation(ctx, executionError(request.ExecutionID, durable.ErrInvalidArgument))
	}
	requestDigest, err := requestHash(request)
	if err != nil {
		return durable.ResumeResult{}, contextOrValidation(ctx, executionError(request.ExecutionID, durable.ErrInvalidArgument))
	}
	var result durable.ResumeResult
	err = backend.withRecord(ctx, request.ExecutionID, "", func(record *durableRecord, now time.Time) error {
		if terminal(record.execution.Status) {
			result = durable.ResumeResult{Execution: cloneJSON(record.execution)}
			return nil
		}
		id := claimReceiptID("resume", request.ClaimID)
		if prior, ok := record.receipts[id]; ok {
			if record.nextToken > prior.leaseToken {
				return executionError(request.ExecutionID, durable.ErrLeaseLost)
			}
			replayed, _, err := getReceipt[durable.ResumeResult](record, id, requestDigest)
			if err != nil {
				return executionError(request.ExecutionID, err)
			}
			if leaseActive(record.execution.Lease, now) && record.execution.Lease.ClaimID == request.ClaimID {
				result = cloneJSON(replayed)
				return nil
			}
		}
		if leaseActive(record.execution.Lease, now) {
			return executionError(request.ExecutionID, durable.ErrBusy)
		}
		reconcile := reconcileForClaim(record)
		record.nextToken++
		record.execution.Status = durable.ExecutionStatusRunning
		record.execution.Lease = &durable.Lease{
			OwnerID:   request.OwnerID,
			ClaimID:   request.ClaimID,
			Token:     record.nextToken,
			ExpiresAt: now.Add(request.LeaseTTL),
		}
		result = durable.ResumeResult{Execution: cloneJSON(record.execution), Reconcile: reconcile}
		return putReceipt(record, id, requestDigest, record.nextToken, result)
	})
	return cloneJSON(result), err
}

func (backend *DurableBackend) RenewExecution(ctx context.Context, request durable.RenewExecutionRequest) (durable.Lease, error) {
	if !validExecutionID(request.ExecutionID) || request.Lease.OwnerID == "" || request.Lease.Token == 0 || request.LeaseTTL <= 0 {
		return durable.Lease{}, contextOrValidation(ctx, executionError(request.ExecutionID, durable.ErrInvalidArgument))
	}
	requestDigest, err := requestHash(request)
	if err != nil {
		return durable.Lease{}, contextOrValidation(ctx, executionError(request.ExecutionID, durable.ErrInvalidArgument))
	}
	var lease durable.Lease
	err = backend.withRecord(ctx, request.ExecutionID, "", func(record *durableRecord, now time.Time) error {
		if record.nextToken > request.Lease.Token {
			return executionError(request.ExecutionID, durable.ErrLeaseLost)
		}
		id := leaseReceiptID("renew", request.Lease, "")
		if replayed, ok, receiptErr := getReceipt[durable.Lease](record, id, requestDigest); ok {
			if receiptErr != nil {
				return executionError(request.ExecutionID, receiptErr)
			}
			if leaseActive(record.execution.Lease, now) {
				record.execution.Lease.ExpiresAt = now.Add(request.LeaseTTL)
			}
			lease = replayed
			return nil
		}
		if err := requireActiveLease(record, request.ExecutionID, request.Lease, now); err != nil {
			return err
		}
		record.execution.Lease.ExpiresAt = now.Add(request.LeaseTTL)
		lease = *record.execution.Lease
		return putReceipt(record, id, requestDigest, request.Lease.Token, lease)
	})
	return lease, err
}

func (backend *DurableBackend) SaveCheckpoint(ctx context.Context, request durable.SaveCheckpointRequest) (durable.Execution, error) {
	if !validExecutionID(request.ExecutionID) || request.Lease.OwnerID == "" || request.Lease.Token == 0 {
		return durable.Execution{}, contextOrValidation(ctx, executionError(request.ExecutionID, durable.ErrInvalidArgument))
	}
	if err := durable.ValidateCheckpoint(request.Checkpoint); err != nil {
		return durable.Execution{}, contextOrValidation(ctx, executionError(request.ExecutionID, err))
	}
	var execution durable.Execution
	err := backend.withRecord(ctx, request.ExecutionID, "", func(record *durableRecord, now time.Time) error {
		if err := requireActiveLease(record, request.ExecutionID, request.Lease, now); err != nil {
			return err
		}
		if current := record.execution.Checkpoint; current != nil {
			if current.Sequence == request.Checkpoint.Sequence {
				if current.ContinuationHash != request.Checkpoint.ContinuationHash {
					return executionError(request.ExecutionID, durable.ErrConflict)
				}
				execution = cloneJSON(record.execution)
				return nil
			}
			if request.Checkpoint.Sequence < current.Sequence {
				return executionError(request.ExecutionID, durable.ErrConflict)
			}
		}
		if record.execution.Version != request.ExpectedVersion {
			return executionError(request.ExecutionID, durable.ErrConflict)
		}
		checkpoint := cloneJSON(request.Checkpoint)
		record.execution.Checkpoint = &checkpoint
		record.execution.Version++
		execution = cloneJSON(record.execution)
		return nil
	})
	return execution, err
}

func (backend *DurableBackend) SuspendExecution(ctx context.Context, request durable.SuspendExecutionRequest) (durable.Execution, error) {
	if !validExecutionID(request.ExecutionID) || request.Lease.OwnerID == "" || request.Lease.Token == 0 {
		return durable.Execution{}, contextOrValidation(ctx, executionError(request.ExecutionID, durable.ErrInvalidArgument))
	}
	requestDigest, err := requestHash(request)
	if err != nil {
		return durable.Execution{}, contextOrValidation(ctx, executionError(request.ExecutionID, durable.ErrInvalidArgument))
	}
	var execution durable.Execution
	err = backend.withRecord(ctx, request.ExecutionID, "", func(record *durableRecord, now time.Time) error {
		if record.nextToken > request.Lease.Token {
			return executionError(request.ExecutionID, durable.ErrLeaseLost)
		}
		id := leaseReceiptID("suspend", request.Lease, fmt.Sprint(request.ExpectedVersion))
		if replayed, ok, err := getReceipt[durable.Execution](record, id, requestDigest); ok {
			if err != nil {
				return executionError(request.ExecutionID, err)
			}
			execution = cloneJSON(replayed)
			return nil
		}
		if err := requireActiveLease(record, request.ExecutionID, request.Lease, now); err != nil {
			return err
		}
		if record.execution.Version != request.ExpectedVersion {
			return executionError(request.ExecutionID, durable.ErrConflict)
		}
		reconcileRunning(record, request.Lease.Token)
		record.execution.Status = durable.ExecutionStatusSuspended
		record.execution.Lease = nil
		record.execution.Version++
		execution = cloneJSON(record.execution)
		return putReceipt(record, id, requestDigest, request.Lease.Token, execution)
	})
	return execution, err
}

func (backend *DurableBackend) FinishExecution(ctx context.Context, request durable.FinishExecutionRequest) (durable.Execution, error) {
	if !validExecutionID(request.ExecutionID) || request.Lease.OwnerID == "" || request.Lease.Token == 0 {
		return durable.Execution{}, contextOrValidation(ctx, executionError(request.ExecutionID, durable.ErrInvalidArgument))
	}
	hash, hashErr := durable.HashResult(request.Result)
	if hashErr != nil {
		return durable.Execution{}, contextOrValidation(ctx, executionError(request.ExecutionID, durable.ErrInvalidArgument))
	}
	if hash != request.ResultHash {
		return durable.Execution{}, contextOrValidation(ctx, executionError(request.ExecutionID, durable.ErrConflict))
	}
	requestDigest, err := requestHash(request)
	if err != nil {
		return durable.Execution{}, contextOrValidation(ctx, executionError(request.ExecutionID, durable.ErrInvalidArgument))
	}
	var execution durable.Execution
	err = backend.withRecord(ctx, request.ExecutionID, "", func(record *durableRecord, now time.Time) error {
		if record.nextToken > request.Lease.Token {
			return executionError(request.ExecutionID, durable.ErrLeaseLost)
		}
		id := leaseReceiptID("finish", request.Lease, fmt.Sprintf("%d:%x", request.ExpectedVersion, request.ResultHash))
		if replayed, ok, err := getReceipt[durable.Execution](record, id, requestDigest); ok {
			if err != nil {
				return executionError(request.ExecutionID, err)
			}
			execution = cloneJSON(replayed)
			return nil
		}
		if terminal(record.execution.Status) {
			return executionError(request.ExecutionID, durable.ErrConflict)
		}
		if err := requireActiveLease(record, request.ExecutionID, request.Lease, now); err != nil {
			return err
		}
		if record.execution.Version != request.ExpectedVersion {
			return executionError(request.ExecutionID, durable.ErrConflict)
		}
		if hasUncertainAttempts(record) {
			return executionError(request.ExecutionID, durable.ErrReconcileRequired)
		}
		result := cloneJSON(request.Result)
		record.execution.Result = &result
		record.execution.ResultHash = request.ResultHash
		record.execution.Status = durable.ExecutionStatusCompleted
		if request.Result.Failure != nil {
			record.execution.Status = durable.ExecutionStatusFailed
		}
		record.execution.Lease = nil
		record.execution.Version++
		execution = cloneJSON(record.execution)
		return putReceipt(record, id, requestDigest, request.Lease.Token, execution)
	})
	return execution, err
}

func (backend *DurableBackend) ReleaseExecution(ctx context.Context, request durable.ReleaseExecutionRequest) (durable.ReleaseResult, error) {
	if !validExecutionID(request.ExecutionID) || request.Lease.OwnerID == "" || request.Lease.Token == 0 {
		return durable.ReleaseResult{}, contextOrValidation(ctx, executionError(request.ExecutionID, durable.ErrInvalidArgument))
	}
	requestDigest, err := requestHash(request)
	if err != nil {
		return durable.ReleaseResult{}, contextOrValidation(ctx, executionError(request.ExecutionID, durable.ErrInvalidArgument))
	}
	var result durable.ReleaseResult
	err = backend.withRecord(ctx, request.ExecutionID, "", func(record *durableRecord, _ time.Time) error {
		if record.nextToken > request.Lease.Token {
			return executionError(request.ExecutionID, durable.ErrLeaseLost)
		}
		id := leaseReceiptID("release", request.Lease, "")
		if replayed, ok, err := getReceipt[durable.ReleaseResult](record, id, requestDigest); ok {
			if err != nil {
				return executionError(request.ExecutionID, err)
			}
			result = cloneJSON(replayed)
			return nil
		}
		if !leaseMatches(record.execution.Lease, request.Lease) {
			return executionError(request.ExecutionID, durable.ErrLeaseLost)
		}
		reconcile := reconcileRunning(record, request.Lease.Token)
		record.execution.Lease = nil
		result = durable.ReleaseResult{Execution: cloneJSON(record.execution), Reconcile: reconcile}
		return putReceipt(record, id, requestDigest, request.Lease.Token, result)
	})
	return cloneJSON(result), err
}

func hasUncertainAttempts(record *durableRecord) bool {
	for _, attempts := range record.attempts {
		for _, attempt := range attempts {
			if attempt.Status == durable.AttemptStatusRunning || attempt.Status == durable.AttemptStatusUnknown {
				return true
			}
		}
	}
	return false
}

func reconcileRunning(record *durableRecord, token uint64) []durable.Attempt {
	var reconciled []durable.Attempt
	for operationID, attempts := range record.attempts {
		for index := range attempts {
			attempt := &attempts[index]
			if attempt.Status != durable.AttemptStatusRunning || attempt.Lease == nil || (token != 0 && attempt.Lease.Token != token) {
				continue
			}
			attempt.Status = durable.AttemptStatusUnknown
			attempt.Lease = nil
			attempt.Version++
			reconciled = append(reconciled, cloneJSON(*attempt))
		}
		record.attempts[operationID] = attempts
	}
	sortAttempts(reconciled)
	return reconciled
}

func reconcileForClaim(record *durableRecord) []durable.Attempt {
	reconcileRunning(record, 0)
	var reconciled []durable.Attempt
	for _, attempts := range record.attempts {
		for _, attempt := range attempts {
			if attempt.Status == durable.AttemptStatusUnknown {
				reconciled = append(reconciled, cloneJSON(attempt))
			}
		}
	}
	sortAttempts(reconciled)
	return reconciled
}

func sortAttempts(attempts []durable.Attempt) {
	sort.Slice(attempts, func(left, right int) bool {
		if attempts[left].OperationID != attempts[right].OperationID {
			return attempts[left].OperationID < attempts[right].OperationID
		}
		return attempts[left].Number < attempts[right].Number
	})
}

func isDurableNotFound(err error) bool {
	return errors.Is(err, durable.ErrNotFound)
}
