package securityscan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type auditOutcome struct {
	worker Worker
	draft  Draft
	result ExecutionResult
	err    error
}

func (s *Service) runDeep(ctx context.Context, scan Scan) error {
	options := scan.Deep
	if err := options.Validate(); err != nil {
		return err
	}
	accepted, aggregate, dispatched, completionSequence, reducedThrough, err := s.recoverDeepState(ctx, scan)
	if err != nil {
		return err
	}
	buffer := append([]Draft(nil), accepted[reducedThrough:]...)
	discoveryCtx, cancelDiscovery := scanBudgetContext(ctx, scan)
	defer cancelDiscovery()
	outcomes := make(chan auditOutcome, options.Workers)
	active := 0
	consecutiveErrors := 0
	noNewStreak := 0
	stopping := false
	stopReason := "capped"

	dispatch := func() {
		for !stopping && active < options.Workers && dispatched < options.MaxDiscoveryRuns {
			dispatched++
			sequence := dispatched
			active++
			go func() {
				worker, draft, result, runErr := s.executeAudit(discoveryCtx, scan, sequence, 1)
				outcomes <- auditOutcome{worker: worker, draft: draft, result: result, err: runErr}
			}()
		}
	}
	updateProgress := func(message string) {
		progress, loadErr := s.store.Progress(context.WithoutCancel(ctx), scan.ID)
		if loadErr != nil {
			progress = Progress{ScanID: scan.ID, FilesTotal: len(scan.Target.Inventory)}
		}
		progress.Phase = PhaseDiscovery
		progress.WorkersPlanned = options.MaxDiscoveryRuns
		progress.WorkersRunning = active
		progress.WorkersDone = len(accepted)
		progress.Message = message
		progress.UpdatedAt = s.now().UTC()
		_ = s.store.SaveProgress(context.WithoutCancel(ctx), progress)
		s.emitProjection(context.WithoutCancel(ctx), scan.ID)
	}
	accept := func(outcome auditOutcome) error {
		completionSequence++
		outcome.worker.Status = WorkerSucceeded
		outcome.worker.CompletionSequence = completionSequence
		outcome.worker.ResultPath = workerResultPath(scan, outcome.worker)
		outcome.worker.CompletedAt, outcome.worker.UpdatedAt = s.now().UTC(), s.now().UTC()
		payload, marshalErr := canonicalJSON(outcome.draft)
		if marshalErr != nil {
			return marshalErr
		}
		if writeErr := atomicWriteScanFile(context.WithoutCancel(ctx), scan.OutputDirectory, outcome.worker.ResultPath, payload); writeErr != nil {
			return writeErr
		}
		if updateErr := s.store.CompleteWorker(context.WithoutCancel(ctx), outcome.worker, outcome.result); updateErr != nil {
			return updateErr
		}
		accumulateUsage(&scan, outcome.result)
		accepted = append(accepted, outcome.draft)
		buffer = append(buffer, outcome.draft)
		return nil
	}
	reduceBuffer := func() error {
		if len(buffer) == 0 {
			return nil
		}
		inputs := make([]Draft, 0, len(buffer)+1)
		if aggregate != nil {
			inputs = append(inputs, cloneDraft(*aggregate))
		}
		inputs = append(inputs, buffer...)
		previousKeys := draftFindingKeys(aggregate)
		reduced, reducerResult, reduceErr := s.reduceWithRetry(discoveryCtx, scan, len(accepted), inputs, options.StopAfterConsecutiveErrors)
		accumulateUsage(&scan, reducerResult)
		if reduceErr != nil {
			return reduceErr
		}
		aggregate = &reduced
		buffer = buffer[:0]
		currentKeys := draftFindingKeys(aggregate)
		if countNewKeys(previousKeys, currentKeys) == 0 {
			noNewStreak++
		} else {
			noNewStreak = 0
		}
		return nil
	}

	if discoveryCtx.Err() != nil {
		stopping, stopReason = true, "deadline"
	} else {
		dispatch()
	}
	updateProgress("independent security audits started")
	for {
		if stopping {
			for active > 0 {
				outcome := <-outcomes
				active--
				if outcome.err == nil {
					if err := accept(outcome); err != nil {
						return err
					}
				} else if usageErr := s.addUsage(ctx, &scan, outcome.result); usageErr != nil && !errors.Is(usageErr, ErrTerminalState) {
					return usageErr
				}
			}
			break
		}
		dispatch()
		if active == 0 {
			break
		}
		var outcome auditOutcome
		select {
		case outcome = <-outcomes:
			active--
		case <-discoveryCtx.Done():
			stopping, stopReason = true, "deadline"
			cancelDiscovery()
			continue
		case <-ctx.Done():
			cancelDiscovery()
			return ctx.Err()
		}
		if outcome.err != nil {
			if usageErr := s.addUsage(ctx, &scan, outcome.result); usageErr != nil {
				return usageErr
			}
			consecutiveErrors++
			updateProgress(fmt.Sprintf("audit worker failed (%d/%d consecutive)", consecutiveErrors, options.StopAfterConsecutiveErrors))
			if consecutiveErrors >= options.StopAfterConsecutiveErrors {
				cancelDiscovery()
				if aggregate == nil {
					return fmt.Errorf("security scan: deep audit stopped after %d consecutive worker errors: %w", consecutiveErrors, outcome.err)
				}
				stopping, stopReason = true, "audit_errors"
			}
			continue
		}
		consecutiveErrors = 0
		if err := accept(outcome); err != nil {
			return err
		}
		updateProgress(fmt.Sprintf("accepted %d independent audits", len(accepted)))
		if err := reduceBuffer(); err != nil {
			if aggregate == nil {
				cancelDiscovery()
				return fmt.Errorf("security scan: deep reducer failed repeatedly: %w", err)
			}
			stopping, stopReason = true, "reducer_error"
			cancelDiscovery()
			continue
		}
		if noNewStreak >= options.StopAfterNoNew {
			stopping, stopReason = true, "saturated"
			cancelDiscovery()
		}
		if dispatched >= options.MaxDiscoveryRuns {
			stopping, stopReason = true, "capped"
		}
	}
	if err := reduceBuffer(); err != nil {
		if aggregate == nil {
			return err
		}
		stopReason = "reducer_error"
	}
	if aggregate == nil && len(accepted) > 0 {
		reduced := cloneDraft(accepted[0])
		aggregate = &reduced
	}
	if aggregate == nil {
		empty := Draft{
			ScanID: scan.ID, Findings: []Finding{},
			Coverage: Coverage{Completeness: CompletenessPartial, Surfaces: []CoverageSurface{}, ExplicitExclusions: []CoverageExclusion{},
				Deferred: []DeferredWork{{ID: "deep_no_completed_review", Reason: "No independent source review completed before Deep Scan stopped."}}},
		}
		aggregate = &empty
	}
	if stopReason == "deadline" || stopReason == "reducer_error" || stopReason == "audit_errors" {
		aggregate.Coverage.Completeness = CompletenessPartial
		aggregate.Coverage.Deferred = append(aggregate.Coverage.Deferred, DeferredWork{ID: "deep_" + stopReason, Reason: "Deep Scan discovery stopped at the configured " + stopReason + "."})
	}
	scan.Warning = "Deep Scan stopped: " + stopReason
	scan.Phase = PhaseReporting
	if err := s.applyTargetDrift(ctx, &scan, aggregate); err != nil {
		return err
	}
	if err := s.enforceDraftIntegrity(ctx, scan, aggregate); err != nil {
		return err
	}
	completion, err := s.finalizer.Finalize(ctx, scan, *aggregate)
	if err != nil {
		return err
	}
	completion.Scan.InputTokens = scan.InputTokens
	completion.Scan.CachedInputTokens = scan.CachedInputTokens
	completion.Scan.OutputTokens = scan.OutputTokens
	completion.Scan.EstimatedCostUSD = scan.EstimatedCostUSD
	if err := s.store.SaveCompletion(context.WithoutCancel(ctx), completion.Scan, completion.Artifacts, completion.Findings.Findings); err != nil {
		return err
	}
	s.emitProjection(context.WithoutCancel(ctx), scan.ID)
	return nil
}

func (s *Service) recoverDeepState(ctx context.Context, scan Scan) ([]Draft, *Draft, int, int, int, error) {
	workers, err := s.store.Workers(ctx, scan.ID)
	if err != nil {
		return nil, nil, 0, 0, 0, err
	}
	type recoveredAudit struct {
		sequence int
		draft    Draft
	}
	var audits []recoveredAudit
	var aggregate *Draft
	latestReducerSequence := -1
	dispatched := 0
	completionSequence := 0
	for _, worker := range workers {
		if worker.CompletionSequence > completionSequence {
			completionSequence = worker.CompletionSequence
		}
	}
	for _, worker := range workers {
		if worker.Kind == WorkerAudit && worker.Sequence > dispatched {
			dispatched = worker.Sequence
		}
		if worker.ResultPath == "" {
			if worker.Status == WorkerRunning || worker.Status == WorkerQueued {
				if err := s.finalizeInterruptedRun(ctx, worker.RunID, false); err != nil {
					return nil, nil, 0, 0, 0, err
				}
				worker.Status = WorkerCanceled
				worker.Error = "interrupted by application restart"
				worker.CompletedAt, worker.UpdatedAt = s.now().UTC(), s.now().UTC()
				if err := s.store.UpdateWorker(context.WithoutCancel(ctx), worker); err != nil {
					return nil, nil, 0, 0, 0, err
				}
			}
			continue
		}
		payload, err := os.ReadFile(filepath.Join(scan.OutputDirectory, filepath.FromSlash(worker.ResultPath)))
		if err != nil {
			return nil, nil, 0, 0, 0, fmt.Errorf("security scan: recover worker artifact %s: %w", worker.ResultPath, err)
		}
		var draft Draft
		if err := json.Unmarshal(payload, &draft); err != nil {
			return nil, nil, 0, 0, 0, fmt.Errorf("security scan: recover worker draft: %w", err)
		}
		if worker.Status != WorkerSucceeded {
			if finalizeErr := s.finalizeInterruptedRun(ctx, worker.RunID, true); finalizeErr != nil {
				return nil, nil, 0, 0, 0, finalizeErr
			}
			worker.Status, worker.Error = WorkerSucceeded, ""
			if worker.Kind == WorkerAudit {
				completionSequence++
				worker.CompletionSequence = completionSequence
			}
			worker.CompletedAt, worker.UpdatedAt = s.now().UTC(), s.now().UTC()
			if err := s.store.CompleteWorker(context.WithoutCancel(ctx), worker, ExecutionResult{}); err != nil {
				return nil, nil, 0, 0, 0, err
			}
		}
		if worker.Kind == WorkerAudit {
			audits = append(audits, recoveredAudit{sequence: worker.CompletionSequence, draft: draft})
		} else if worker.Kind == WorkerReducer && worker.Sequence >= latestReducerSequence {
			recovered := cloneDraft(draft)
			aggregate = &recovered
			latestReducerSequence = worker.Sequence
		}
	}
	sort.Slice(audits, func(i, j int) bool { return audits[i].sequence < audits[j].sequence })
	accepted := make([]Draft, len(audits))
	for index, audit := range audits {
		accepted[index] = audit.draft
	}
	reducedThrough := latestReducerSequence
	if reducedThrough < 0 {
		reducedThrough = 0
	}
	if reducedThrough > len(accepted) {
		reducedThrough = len(accepted)
	}
	return accepted, aggregate, dispatched, completionSequence, reducedThrough, nil
}

func (s *Service) reduceWithRetry(ctx context.Context, scan Scan, sequence int, inputs []Draft, maxAttempts int) (Draft, ExecutionResult, error) {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	firstAttempt, err := s.nextWorkerAttempt(ctx, scan.ID, WorkerReducer, sequence)
	if err != nil {
		return Draft{}, ExecutionResult{}, err
	}
	var usage ExecutionResult
	var lastErr error
	for offset := range maxAttempts {
		attempt := firstAttempt + offset
		draft, result, err := s.executeReducer(ctx, scan, sequence, attempt, inputs)
		usage.InputTokens += result.InputTokens
		usage.CachedInputTokens += result.CachedInputTokens
		usage.OutputTokens += result.OutputTokens
		usage.EstimatedCostUSD += result.EstimatedCostUSD
		usage.RunID = result.RunID
		if err == nil {
			return draft, usage, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return Draft{}, usage, lastErr
}

func (s *Service) nextWorkerAttempt(ctx context.Context, scanID string, kind WorkerKind, sequence int) (int, error) {
	workers, err := s.store.Workers(ctx, scanID)
	if err != nil {
		return 0, err
	}
	next := 1
	for _, worker := range workers {
		if worker.Kind == kind && worker.Sequence == sequence && worker.Attempt >= next {
			next = worker.Attempt + 1
		}
	}
	return next, nil
}

func (s *Service) executeReducer(ctx context.Context, scan Scan, sequence, attempt int, inputs []Draft) (Draft, ExecutionResult, error) {
	workerID, err := NewID("worker")
	if err != nil {
		return Draft{}, ExecutionResult{}, err
	}
	now := s.now().UTC()
	reducerRoute := routeFallback(scan.ReducerRoute, scan.Route)
	worker := Worker{ID: workerID, ScanID: scan.ID, Kind: WorkerReducer, Status: WorkerQueued, Sequence: sequence, Attempt: attempt, Route: reducerRoute, UpdatedAt: now}
	if err := s.store.CreateWorker(ctx, worker); err != nil {
		return Draft{}, ExecutionResult{}, err
	}
	key := bindingKey(scan.ID, worker.ID)
	s.mu.Lock()
	s.reducerData[key] = cloneDraftSlice(inputs)
	s.mu.Unlock()
	worker.Status, worker.StartedAt, worker.UpdatedAt = WorkerRunning, now, now
	if err := s.store.UpdateWorker(ctx, worker); err != nil {
		return Draft{}, ExecutionResult{}, err
	}
	result, runErr := s.executor.Execute(ctx, ExecutionRequest{
		Scan: scan, Worker: worker, Prompt: "Reduce the host-assigned Deep Scan drafts.", Instructions: ReducerInstructions,
		WorkspaceRoot: scan.Target.SnapshotRoot, Route: reducerRoute, Budget: scanExecutionBudget(scan), ReducerInputs: inputs,
	})
	worker.RunID = result.RunID
	if runErr != nil {
		worker.Status, worker.Error, worker.CompletedAt, worker.UpdatedAt = WorkerFailed, runErr.Error(), s.now().UTC(), s.now().UTC()
		_ = s.store.UpdateWorker(context.WithoutCancel(ctx), worker)
		if usageErr := s.store.AddUsage(context.WithoutCancel(ctx), scan.ID, result); usageErr != nil && !errors.Is(usageErr, ErrTerminalState) {
			runErr = errors.Join(runErr, usageErr)
		}
		s.mu.Lock()
		delete(s.reducerData, key)
		s.mu.Unlock()
		return Draft{}, result, runErr
	}
	s.mu.Lock()
	draft, ok := s.drafts[key]
	delete(s.drafts, key)
	delete(s.draftCounts, key)
	delete(s.reducerData, key)
	s.mu.Unlock()
	if !ok {
		worker.Status, worker.Error, worker.CompletedAt, worker.UpdatedAt = WorkerFailed, "reducer completed without an accepted reduction", s.now().UTC(), s.now().UTC()
		_ = s.store.UpdateWorker(context.WithoutCancel(ctx), worker)
		if usageErr := s.store.AddUsage(context.WithoutCancel(ctx), scan.ID, result); usageErr != nil && !errors.Is(usageErr, ErrTerminalState) {
			return Draft{}, result, usageErr
		}
		return Draft{}, result, fmt.Errorf("security scan: reducer completed without an accepted reduction")
	}
	worker.Status, worker.CompletedAt, worker.UpdatedAt = WorkerSucceeded, s.now().UTC(), s.now().UTC()
	worker.ResultPath = workerResultPath(scan, worker)
	payload, marshalErr := canonicalJSON(draft)
	if marshalErr != nil {
		return Draft{}, result, marshalErr
	}
	if writeErr := atomicWriteScanFile(context.WithoutCancel(ctx), scan.OutputDirectory, worker.ResultPath, payload); writeErr != nil {
		return Draft{}, result, writeErr
	}
	if err := s.store.CompleteWorker(context.WithoutCancel(ctx), worker, result); err != nil {
		return Draft{}, result, err
	}
	return draft, result, nil
}

func cloneDraftSlice(values []Draft) []Draft {
	cloned := make([]Draft, len(values))
	for index, value := range values {
		cloned[index] = cloneDraft(value)
	}
	return cloned
}

func draftFindingKeys(draft *Draft) map[string]struct{} {
	keys := map[string]struct{}{}
	if draft == nil {
		return keys
	}
	for _, finding := range draft.Findings {
		key := finding.RuleID + "\x00" + finding.Identity.Anchor + "\x00" + finding.Identity.Instance
		keys[key] = struct{}{}
	}
	return keys
}

func countNewKeys(previous, current map[string]struct{}) int {
	count := 0
	for key := range current {
		if _, exists := previous[key]; !exists {
			count++
		}
	}
	return count
}

func normalizeReduction(draft Draft) Draft {
	draft.Findings = append([]Finding(nil), draft.Findings...)
	sort.SliceStable(draft.Findings, func(i, j int) bool {
		left := draft.Findings[i].RuleID + draft.Findings[i].Identity.Anchor
		right := draft.Findings[j].RuleID + draft.Findings[j].Identity.Anchor
		return left < right
	})
	return draft
}

var _ = json.Valid
var _ = errors.Is
