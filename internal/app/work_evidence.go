package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"

	"github.com/Viking602/azem/internal/observation"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/azem/internal/verification"
	"github.com/Viking602/azem/internal/workrevision"
)

const (
	workSpecArtifactPrefix = session.InternalArtifactKindPrefix + "work_spec_v1:"
	workPlanArtifactPrefix = session.InternalArtifactKindPrefix + "verification_plan_v1_live:"
)

var runtimeEvidenceSessionLocks sync.Map

type runtimeEvidenceSnapshot struct {
	work             session.WorkSpecV1
	revision         session.WorkRevisionV1
	plan             session.VerificationPlanV1
	todo             session.TodoList
	records          []session.ToolRecord
	goalSource       session.SourceRefV1
	mutating         bool
	latestMutationAt time.Time
	captureErrors    []string
}

type runtimeCheckState struct {
	status   string
	missing  []string
	evidence []session.SourceRefV1
}

func runtimeEvidenceLock(sessions *session.Service, sessionID string) *sync.Mutex {
	key := fmt.Sprintf("%p:%s", sessions, sessionID)
	value, _ := runtimeEvidenceSessionLocks.LoadOrStore(key, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func persistToolIntent(ctx context.Context, sessions *session.Service, workspace, sessionID, runID string, call message.ToolCall) (session.ActionIntentV1, error) {
	if sessions == nil {
		return session.ActionIntentV1{}, nil
	}
	lock := runtimeEvidenceLock(sessions, sessionID)
	lock.Lock()
	defer lock.Unlock()

	snapshot, err := deriveRuntimeEvidence(ctx, sessions, workspace, sessionID, runID, []string{runID})
	if err != nil {
		return session.ActionIntentV1{}, err
	}
	if err := persistRuntimeContracts(ctx, sessions, sessionID, runID, snapshot); err != nil {
		return session.ActionIntentV1{}, err
	}
	argumentsHash := ""
	if len(call.Arguments) > 0 {
		digest := sha256.Sum256(call.Arguments)
		argumentsHash = hex.EncodeToString(digest[:])
	}
	kind := "tool"
	if strings.HasPrefix(call.Name, "subagent.") {
		kind = "subagent"
	}
	intent := session.ActionIntentV1{
		Version:    session.WorkContractVersionV1,
		ID:         "intent:" + shortEvidenceHash(sessionID+"\x00"+runID+"\x00"+call.ID+"\x00"+call.Name),
		WorkSpecID: snapshot.work.ID, SessionID: sessionID, RunID: runID, ToolCallID: call.ID,
		Kind: kind, Name: call.Name, ArgumentsHash: argumentsHash,
		Sources: []session.SourceRefV1{snapshot.goalSource}, CreatedAt: time.Now().UTC(),
	}
	intent, err = workrevision.BindIntent(snapshot.revision, intent)
	if err != nil {
		return session.ActionIntentV1{}, err
	}
	store, err := workrevision.NewStore(sessions, sessionID, runID)
	if err != nil {
		return session.ActionIntentV1{}, err
	}
	if err := store.SaveIntent(ctx, intent); err != nil {
		return session.ActionIntentV1{}, err
	}
	return intent, nil
}

func loadToolIntent(ctx context.Context, sessions *session.Service, sessionID, runID string, call message.ToolCall) (session.ActionIntentV1, error) {
	store, err := workrevision.NewStore(sessions, sessionID, runID)
	if err != nil {
		return session.ActionIntentV1{}, err
	}
	intentID := "intent:" + shortEvidenceHash(sessionID+"\x00"+runID+"\x00"+call.ID+"\x00"+call.Name)
	return store.Intent(ctx, intentID)
}

func persistToolObservation(ctx context.Context, sessions *session.Service, workspace, sessionID, runID string, intent session.ActionIntentV1, record session.ToolRecord) error {
	if sessions == nil || intent.ID == "" {
		return nil
	}
	lock := runtimeEvidenceLock(sessions, sessionID)
	lock.Lock()
	defer lock.Unlock()

	snapshot, err := deriveRuntimeEvidence(ctx, sessions, workspace, sessionID, runID, []string{runID})
	if err != nil {
		return err
	}
	if err := persistRuntimeContracts(ctx, sessions, sessionID, runID, snapshot); err != nil {
		return err
	}
	store, err := workrevision.NewStore(sessions, sessionID, runID)
	if err != nil {
		return err
	}
	sourceRevision, err := store.Revision(ctx, intent.RevisionID)
	if err != nil {
		return fmt.Errorf("load tool intent revision: %w", err)
	}
	available, spilled := true, record.ArtifactID != ""
	metadata := observation.ObservationMetadata{RawAvailable: &available, Spilled: &spilled}
	metadata.ExitStatus, metadata.TimedOut = toolExitStatus(record.Structured)
	metadata.Cancelled = record.State == session.ToolInterrupted
	envelope := observation.NormalizeToolObservation(record, metadata)
	envelope.IntentID = intent.ID
	envelope.RevisionID = snapshot.revision.ID
	envelope.SnapshotHash = snapshot.revision.SnapshotHash
	bindBeforeHashes(envelope.Files, sourceRevision.Files)
	if err := envelope.Validate(); err != nil {
		return err
	}
	if err := store.SaveObservation(ctx, envelope); err != nil {
		return err
	}
	disposition, err := workrevision.Disposition(workrevision.CompatibilityInput{
		IntentID: intent.ID, Source: sourceRevision, Current: snapshot.revision,
		ResultFiles: envelope.Files, CompletedAt: record.CompletedAt,
	})
	if err != nil {
		return err
	}
	return store.SaveDisposition(ctx, disposition)
}

func newMutatingVerificationGuardrail(sessions *session.Service, workspace, sessionID, runID string, enforceSessionTodo bool, relatedRunIDs func() []string) hyagent.OutputGuardrail {
	if sessions == nil || strings.TrimSpace(workspace) == "" {
		return hyagent.NewOutputGuardrail("current-work-verification", func(context.Context, hyagent.OutputGuardrailInput) (hyagent.OutputGuardrailResult, error) {
			return hyagent.AllowOutput(), nil
		})
	}
	resultStore, storeErr := verification.NewArtifactResultStore(sessions, sessionID, runID)
	guard, guardErr := verification.NewFinalClaimGuard(resultStore, nil)
	return hyagent.NewOutputGuardrail("current-work-verification", func(ctx context.Context, _ hyagent.OutputGuardrailInput) (hyagent.OutputGuardrailResult, error) {
		if storeErr != nil {
			return hyagent.OutputGuardrailResult{}, storeErr
		}
		if guardErr != nil {
			return hyagent.OutputGuardrailResult{}, guardErr
		}
		runIDs := []string{runID}
		if relatedRunIDs != nil {
			runIDs = relatedRunIDs()
		}
		lock := runtimeEvidenceLock(sessions, sessionID)
		lock.Lock()
		defer lock.Unlock()

		snapshot, err := deriveRuntimeEvidence(ctx, sessions, workspace, sessionID, runID, runIDs)
		if err != nil {
			return hyagent.OutputGuardrailResult{}, err
		}
		if err := persistRuntimeContracts(ctx, sessions, sessionID, runID, snapshot); err != nil {
			return hyagent.OutputGuardrailResult{}, err
		}
		if items := guardrailTodoItems(snapshot.todo, enforceSessionTodo); len(items) > 0 {
			return hyagent.RetryOutputWithPolicy(hyagent.RetryPolicy{IncludeRejectedOutput: true}, message.NewText(message.RoleUser, unfinishedTodoRetryMessage(items))), nil
		}
		if !snapshot.mutating {
			return hyagent.AllowOutput(), nil
		}
		state := evaluateRuntimeChecks(snapshot)
		if len(snapshot.captureErrors) > 0 {
			state.missing = append(state.missing, snapshot.captureErrors...)
			state.status = ""
		}
		if state.status == "pass" || state.status == "fail" {
			result := runtimeVerificationResult(snapshot, state.status, state.evidence, time.Now().UTC())
			if err := resultStore.Save(ctx, result); err != nil {
				return hyagent.OutputGuardrailResult{}, err
			}
		}
		decision, err := guard.Check(ctx, true, snapshot.work, snapshot.plan, snapshot.revision.SnapshotHash)
		if err != nil {
			return hyagent.OutputGuardrailResult{}, err
		}
		switch decision.Action {
		case "allow":
			return hyagent.AllowOutput(), nil
		case "retry":
			return hyagent.RetryOutputWithPolicy(hyagent.RetryPolicy{IncludeRejectedOutput: true}, message.NewText(message.RoleUser, verificationRetryMessage(snapshot, state.missing))), nil
		case "surface":
			return surfaceVerificationOutput(decision.Reason), nil
		default:
			return hyagent.BlockOutput("invalid verification guard decision"), nil
		}
	})
}

// surfaceVerificationOutput blocks an unverified terminal claim without
// rewriting the model-authored message as host prose. The candidate output
// remains attached to Venat's guardrail error while the run finishes failed.
func surfaceVerificationOutput(reason string) hyagent.OutputGuardrailResult {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Verification evidence is missing, stale, or failed for the current workspace snapshot."
	}
	return hyagent.BlockOutput(reason)
}

func deriveRuntimeEvidence(ctx context.Context, sessions *session.Service, workspace, sessionID, runID string, relatedRunIDs []string) (runtimeEvidenceSnapshot, error) {
	projection, err := sessions.LoadProjection(ctx, sessionID)
	if err != nil {
		return runtimeEvidenceSnapshot{}, fmt.Errorf("load work evidence projection: %w", err)
	}
	todo, err := sessions.LoadTodo(ctx, sessionID)
	if err != nil {
		return runtimeEvidenceSnapshot{}, fmt.Errorf("load work evidence todo: %w", err)
	}
	goal, goalSource, sequence := runtimeGoal(projection.Blocks, runID)
	createdAt := projection.Session.CreatedAt

	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	placeholderWork, _, err := verification.CompileCriteria(verification.CompileInput{
		SessionID: sessionID, RunID: runID, Goal: goal, GoalSource: goalSource, Todo: todo,
		RevisionID: "pending", SnapshotHash: "pending", CreatedAt: createdAt,
	})
	if err != nil {
		return runtimeEvidenceSnapshot{}, err
	}
	records := recordsForRuns(projection.ToolRecords, relatedRunIDs)
	files, mutating, latestMutationAt, captureErrors := runtimeRevisionFiles(workspace, records)
	revision, err := workrevision.Derive(workrevision.DeriveInput{
		SessionID: sessionID, CanonicalUserSequence: sequence, TodoRevision: todo.Revision,
		SemanticRevision: 0, ExplicitConstraints: placeholderWork.Criteria,
		Files: files, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return runtimeEvidenceSnapshot{}, err
	}
	work, plan, err := verification.CompileCriteria(verification.CompileInput{
		SessionID: sessionID, RunID: runID, Goal: goal, GoalSource: goalSource, Todo: todo,
		RevisionID: revision.ID, SnapshotHash: revision.SnapshotHash, CreatedAt: revision.CreatedAt,
	})
	if err != nil {
		return runtimeEvidenceSnapshot{}, err
	}
	touched := make([]verification.TouchedFileV1, 0, len(revision.Files))
	for _, file := range revision.Files {
		if file.Touched {
			touched = append(touched, verification.TouchedFileV1{Path: file.Path, Change: "modified"})
		}
	}
	if strings.TrimSpace(workspace) != "" {
		plan, err = verification.SelectDeterministicChecks(verification.SelectInput{Workspace: workspace, Work: work, Plan: plan, Touched: touched})
		if err != nil {
			return runtimeEvidenceSnapshot{}, err
		}
	}
	if mutating {
		plan = addRuntimeReadbackChecks(plan, revision.Files, workspace)
	}
	return runtimeEvidenceSnapshot{
		work: work, revision: revision, plan: plan, todo: todo, records: records, goalSource: goalSource,
		mutating: mutating, latestMutationAt: latestMutationAt, captureErrors: captureErrors,
	}, nil
}

func runtimeEvidenceStatus(ctx context.Context, sessions *session.Service, workspace, sessionID, runID string, relatedRunIDs []string) string {
	if sessions == nil {
		return ""
	}
	lock := runtimeEvidenceLock(sessions, sessionID)
	lock.Lock()
	defer lock.Unlock()
	snapshot, err := deriveRuntimeEvidence(ctx, sessions, workspace, sessionID, runID, relatedRunIDs)
	if err != nil || !snapshot.mutating {
		return ""
	}
	store, err := verification.NewArtifactResultStore(sessions, sessionID, runID)
	if err != nil {
		return ""
	}
	result, err := store.Latest(ctx, snapshot.work.ID)
	if errors.Is(err, verification.ErrNoVerificationResult) {
		return "provisional"
	}
	if err != nil || verification.CompatibleResult(snapshot.work, snapshot.plan, result, snapshot.revision.SnapshotHash) != nil {
		return "stale"
	}
	if result.Status == "pass" {
		return "verified"
	}
	return "stale"
}

type persistedRunEvidenceStatusV1 struct {
	Version int    `json:"version"`
	RunID   string `json:"run_id"`
	Status  string `json:"status"`
}

func persistRunEvidenceStatus(ctx context.Context, sessions *session.Service, sessionID, runID, status string) error {
	if sessions == nil || runID == "" || projectedEvidenceStatus(status) == "" {
		return nil
	}
	value := persistedRunEvidenceStatusV1{Version: 1, RunID: runID, Status: status}
	return putEvidenceArtifactIfChanged(
		ctx, sessions, sessionID, runID,
		session.InternalArtifactKindPrefix+"run_evidence_status_v1:"+shortEvidenceHash(runID), status, value,
	)
}

func loadRunEvidenceStatus(ctx context.Context, sessions *session.Service, sessionID, runID string) string {
	if sessions == nil || runID == "" {
		return ""
	}
	artifact, err := sessions.LoadLatestArtifactByKind(ctx, sessionID, session.InternalArtifactKindPrefix+"run_evidence_status_v1:"+shortEvidenceHash(runID))
	if err != nil {
		return ""
	}
	var value persistedRunEvidenceStatusV1
	if json.Unmarshal(artifact.Payload, &value) != nil || value.Version != 1 || value.RunID != runID {
		return ""
	}
	return projectedEvidenceStatus(value.Status)
}

func persistRuntimeContracts(ctx context.Context, sessions *session.Service, sessionID, runID string, snapshot runtimeEvidenceSnapshot) error {
	store, err := workrevision.NewStore(sessions, sessionID, runID)
	if err != nil {
		return err
	}
	latest, latestErr := store.LatestRevision(ctx)
	if latestErr != nil || latest.ID != snapshot.revision.ID {
		if err := store.SaveRevision(ctx, snapshot.revision); err != nil {
			return err
		}
	}
	if err := putEvidenceArtifactIfChanged(ctx, sessions, sessionID, runID, workSpecArtifactPrefix+shortEvidenceHash(snapshot.work.ID), snapshot.work.ID, snapshot.work); err != nil {
		return err
	}
	if err := store.SaveVerificationPlan(ctx, snapshot.plan); err != nil {
		return err
	}
	return putEvidenceArtifactIfChanged(ctx, sessions, sessionID, runID, workPlanArtifactPrefix+shortEvidenceHash(snapshot.plan.ID), snapshot.plan.ID, snapshot.plan)
}

func putEvidenceArtifactIfChanged(ctx context.Context, sessions *session.Service, sessionID, runID, kind, preview string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	current, loadErr := sessions.LoadLatestArtifactByKind(ctx, sessionID, kind)
	if loadErr == nil && bytes.Equal(current.Payload, payload) {
		return nil
	}
	if loadErr != nil && !errors.Is(loadErr, session.ErrContextArtifactNotFound) {
		return loadErr
	}
	_, err = sessions.PutArtifact(ctx, sessionID, runID, kind, payload, preview)
	return err
}

func runtimeGoal(blocks []session.Block, runID string) (string, session.SourceRefV1, int64) {
	var goal session.Block
	var latestUserSequence int64
	for _, block := range blocks {
		if block.Kind != "user" {
			continue
		}
		if block.Sequence > latestUserSequence {
			latestUserSequence = block.Sequence
		}
		if block.RunID == runID && strings.TrimSpace(block.Content) != "" {
			goal = block
		}
	}
	if strings.TrimSpace(goal.Content) == "" {
		for index := len(blocks) - 1; index >= 0; index-- {
			if blocks[index].Kind == "user" && strings.TrimSpace(blocks[index].Content) != "" {
				goal = blocks[index]
				break
			}
		}
	}
	if strings.TrimSpace(goal.Content) == "" {
		return "Complete run " + runID, session.SourceRefV1{Kind: "run", ID: runID}, latestUserSequence
	}
	return strings.TrimSpace(goal.Content), session.SourceRefV1{Kind: "session_block", ID: strconv.FormatInt(goal.Sequence, 10)}, latestUserSequence
}

func recordsForRuns(records []session.ToolRecord, runIDs []string) []session.ToolRecord {
	allowed := make(map[string]struct{}, len(runIDs)+1)
	for _, id := range runIDs {
		if id != "" {
			allowed[id] = struct{}{}
		}
	}
	result := make([]session.ToolRecord, 0)
	for _, record := range records {
		if _, ok := allowed[record.RunID]; ok {
			result = append(result, record)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].StartedAt.Equal(result[j].StartedAt) {
			return result[i].ToolCallID < result[j].ToolCallID
		}
		return result[i].StartedAt.Before(result[j].StartedAt)
	})
	return result
}

func runtimeRevisionFiles(workspace string, records []session.ToolRecord) ([]session.WorkRevisionFileV1, bool, time.Time, []string) {
	type ownership struct{ observed, touched bool }
	owned := make(map[string]ownership)
	mutating := false
	var latestMutationAt time.Time
	for _, record := range records {
		if record.State != session.ToolCompleted {
			continue
		}
		recordMutated := toolRecordMutated(record)
		if recordMutated {
			mutating = true
			if record.CompletedAt.After(latestMutationAt) {
				latestMutationAt = record.CompletedAt
			}
		}
		for _, observation := range record.Observations {
			path := filepath.ToSlash(filepath.Clean(strings.TrimSpace(observation.Path)))
			if path == "" || path == "." || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, "../") {
				continue
			}
			entry := owned[path]
			if observation.Operation == "read" || observation.Operation == "format" && !recordMutated {
				entry.observed = true
			} else {
				entry.touched = true
				mutating = true
				if record.CompletedAt.After(latestMutationAt) {
					latestMutationAt = record.CompletedAt
				}
			}
			owned[path] = entry
		}
	}
	paths := make([]string, 0, len(owned))
	for path := range owned {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	remaining := int64(maxWorkspaceTotalBytes)
	files := make([]session.WorkRevisionFileV1, 0, len(paths))
	captureErrors := make([]string, 0)
	for _, path := range paths {
		payload, err := readWorkspaceEvidence(workspace, path, &remaining)
		digest := ""
		if err == nil {
			digest = sha256Hex(payload)
		} else if os.IsNotExist(err) {
			digest = sha256Hex([]byte("deleted\x00" + path))
		} else {
			captureErrors = append(captureErrors, "Capture current bytes for "+path)
			continue
		}
		files = append(files, session.WorkRevisionFileV1{Path: path, SHA256: digest, Observed: owned[path].observed, Touched: owned[path].touched})
	}
	return files, mutating, latestMutationAt, captureErrors
}

func toolRecordMutated(record session.ToolRecord) bool {
	switch record.Name {
	case "coding.edit_hashline", "coding.replace", "coding.write_file", "coding.delete_file":
		return true
	case "coding.gofmt":
		var result struct {
			Changed *bool `json:"changed"`
		}
		if json.Unmarshal(record.Structured, &result) == nil && result.Changed != nil {
			return *result.Changed
		}
		return !strings.Contains(strings.ToLower(record.Content), "already formatted")
	default:
		return false
	}
}

func addRuntimeReadbackChecks(plan session.VerificationPlanV1, files []session.WorkRevisionFileV1, workspace string) session.VerificationPlanV1 {
	criterionIDs := make([]string, 0)
	seenCriteria := make(map[string]struct{})
	for _, check := range plan.Checks {
		for _, id := range check.CriterionIDs {
			if _, exists := seenCriteria[id]; !exists {
				seenCriteria[id] = struct{}{}
				criterionIDs = append(criterionIDs, id)
			}
		}
	}
	touched := make([]string, 0)
	for _, file := range files {
		if file.Touched {
			touched = append(touched, file.Path)
			plan.Checks = append([]session.VerificationCheckV1{{
				ID: "readback:" + file.Path, CriterionIDs: append([]string(nil), criterionIDs...),
				Kind: "artifact", ArtifactRef: "file:" + file.Path,
			}}, plan.Checks...)
		}
	}
	if len(touched) > 0 && workspaceHasGitMetadata(workspace) {
		command := append([]string{"git", "diff", "--check", "--"}, touched...)
		plan.Checks = append([]session.VerificationCheckV1{{
			ID: "git-diff-check", CriterionIDs: append([]string(nil), criterionIDs...), Kind: "command", Command: command, TimeoutMS: 60_000,
		}}, plan.Checks...)
	}
	return plan
}

func workspaceHasGitMetadata(workspace string) bool {
	current := filepath.Clean(strings.TrimSpace(workspace))
	for current != "" && current != "." {
		if info, err := os.Stat(filepath.Join(current, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return true
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return false
}

func evaluateRuntimeChecks(snapshot runtimeEvidenceSnapshot) runtimeCheckState {
	state := runtimeCheckState{status: "pass"}
	for _, check := range snapshot.plan.Checks {
		if check.Kind == "criterion" {
			continue
		}
		var record *session.ToolRecord
		if check.Kind == "command" {
			record = matchingCommandRecord(check, snapshot.records, snapshot.latestMutationAt)
			if record == nil {
				if formatterRecords, formatterCheck := matchingGofmtRecords(check, snapshot.revision.Files, snapshot.records); formatterCheck {
					if len(formatterRecords) == 0 {
						state.status = ""
						state.missing = append(state.missing, checkInstruction(check))
					} else {
						for _, formatterRecord := range formatterRecords {
							state.evidence = appendUniqueSource(state.evidence, session.SourceRefV1{
								Kind: "tool_record", ID: formatterRecord.RunID + ":" + formatterRecord.ToolCallID,
							})
						}
					}
					continue
				}
			}
		} else if check.Kind == "artifact" && strings.HasPrefix(check.ArtifactRef, "file:") {
			path := strings.TrimPrefix(check.ArtifactRef, "file:")
			record = matchingReadbackRecord(path, snapshot.revision.Files, snapshot.records, snapshot.latestMutationAt)
		}
		if record == nil {
			state.status = ""
			state.missing = append(state.missing, checkInstruction(check))
			continue
		}
		state.evidence = appendUniqueSource(state.evidence, session.SourceRefV1{Kind: "tool_record", ID: record.RunID + ":" + record.ToolCallID})
		if record.State != session.ToolCompleted || commandOutputMustBeEmpty(check) && strings.TrimSpace(structuredToolOutput(*record)) != "" {
			state.status = "fail"
		}
	}
	if len(state.missing) > 0 && state.status != "fail" {
		state.status = ""
	}
	return state
}

func matchingCommandRecord(check session.VerificationCheckV1, records []session.ToolRecord, after time.Time) *session.ToolRecord {
	expected := shellCommandForCheck(check)
	var matched *session.ToolRecord
	for index := range records {
		record := &records[index]
		if record.StartedAt.Before(after) || record.State == session.ToolRunning {
			continue
		}
		if toolCommand(*record) == expected {
			matched = record
		}
	}
	return matched
}

func matchingGofmtRecords(check session.VerificationCheckV1, files []session.WorkRevisionFileV1, records []session.ToolRecord) ([]*session.ToolRecord, bool) {
	if len(check.Command) < 3 || filepath.Base(check.Command[0]) != "gofmt" || check.Command[1] != "-d" {
		return nil, false
	}
	currentSHA := make(map[string]string, len(files))
	for _, file := range files {
		currentSHA[filepath.ToSlash(filepath.Clean(file.Path))] = file.SHA256
	}
	matches := make([]*session.ToolRecord, 0, len(check.Command)-2)
	seen := make(map[string]struct{}, len(check.Command)-2)
	for _, rawPath := range check.Command[2:] {
		path := filepath.ToSlash(filepath.Clean(rawPath))
		if path == "." {
			return nil, true
		}
		if _, duplicate := seen[path]; duplicate {
			continue
		}
		seen[path] = struct{}{}
		sha := currentSHA[path]
		if sha == "" {
			return nil, true
		}
		var matched *session.ToolRecord
		for index := range records {
			record := &records[index]
			if record.Name != "coding.gofmt" || record.State != session.ToolCompleted {
				continue
			}
			var result struct {
				Path string `json:"path"`
			}
			if json.Unmarshal(record.Structured, &result) != nil || filepath.ToSlash(filepath.Clean(result.Path)) != path {
				continue
			}
			for _, observation := range record.Observations {
				if observation.Operation == "format" && filepath.ToSlash(filepath.Clean(observation.Path)) == path && observation.SHA256 == sha {
					if matched == nil || matched.CompletedAt.Before(record.CompletedAt) {
						matched = record
					}
					break
				}
			}
		}
		if matched == nil {
			return nil, true
		}
		matches = append(matches, matched)
	}
	return matches, true
}

func matchingReadbackRecord(path string, files []session.WorkRevisionFileV1, records []session.ToolRecord, after time.Time) *session.ToolRecord {
	sha := ""
	for _, file := range files {
		if file.Path == path {
			sha = file.SHA256
			break
		}
	}
	var matched *session.ToolRecord
	for index := range records {
		record := &records[index]
		if record.State != session.ToolCompleted || record.StartedAt.Before(after) {
			continue
		}
		for _, current := range record.Observations {
			if current.Operation == "read" && filepath.ToSlash(filepath.Clean(current.Path)) == path && current.SHA256 == sha {
				matched = record
			}
		}
	}
	return matched
}

func toolCommand(record session.ToolRecord) string {
	if record.Name == "coding.shell" {
		var input struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(record.Arguments, &input) == nil {
			return strings.TrimSpace(input.Command)
		}
	}
	if record.Name == "coding.go_test" {
		var input struct {
			Args []string          `json:"args"`
			Env  map[string]string `json:"env"`
		}
		if json.Unmarshal(record.Arguments, &input) == nil {
			return shellCommand(input.Env, "", input.Args)
		}
	}
	return ""
}

func shellCommandForCheck(check session.VerificationCheckV1) string {
	return shellCommand(check.Environment, check.CWD, check.Command)
}

func shellCommand(environment map[string]string, cwd string, command []string) string {
	parts := make([]string, 0, len(environment)+len(command))
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, key+"="+shellQuote(environment[key]))
	}
	for _, value := range command {
		parts = append(parts, shellQuote(value))
	}
	joined := strings.Join(parts, " ")
	if strings.TrimSpace(cwd) != "" {
		return "(cd " + shellQuote(cwd) + " && " + joined + ")"
	}
	return joined
}

func shellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_./:@%+=,-", r))
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func checkInstruction(check session.VerificationCheckV1) string {
	if check.Kind == "command" {
		return shellCommandForCheck(check)
	}
	if strings.HasPrefix(check.ArtifactRef, "file:") {
		return "Read back " + strings.TrimPrefix(check.ArtifactRef, "file:") + " with coding.read_file"
	}
	return check.ID
}

func commandOutputMustBeEmpty(check session.VerificationCheckV1) bool {
	return check.ID == "gofmt" || check.ID == "git-diff-check"
}

func structuredToolOutput(record session.ToolRecord) string {
	var value struct {
		Output string `json:"output"`
	}
	if json.Unmarshal(record.Structured, &value) == nil {
		return value.Output
	}
	return record.Content
}

func toolExitStatus(raw json.RawMessage) (*int, bool) {
	var value struct {
		ExitCode int    `json:"exitCode"`
		Status   string `json:"status"`
		Reason   string `json:"reason"`
	}
	if json.Unmarshal(raw, &value) != nil || len(raw) == 0 {
		return nil, false
	}
	exit := value.ExitCode
	timedOut := value.Reason == "timeout" || value.Reason == "idle_timeout" || value.Status == "timeout"
	return &exit, timedOut
}

func bindBeforeHashes(files []session.FileObservationV1, source []session.WorkRevisionFileV1) {
	before := make(map[string]string, len(source))
	for _, file := range source {
		before[file.Path] = file.SHA256
	}
	for index := range files {
		if files[index].State == "modified" || files[index].State == "deleted" {
			files[index].BeforeSHA256 = before[files[index].Path]
		}
	}
}

func runtimeVerificationResult(snapshot runtimeEvidenceSnapshot, status string, evidence []session.SourceRefV1, completedAt time.Time) session.VerificationResultV1 {
	criteria := make([]session.CriterionResultV1, 0, len(snapshot.work.Criteria))
	for _, criterion := range snapshot.work.Criteria {
		criteria = append(criteria, session.CriterionResultV1{CriterionID: criterion.ID, Status: status, Evidence: append([]session.SourceRefV1(nil), evidence...)})
	}
	return session.VerificationResultV1{
		Version: session.WorkContractVersionV1,
		ID:      "verification-result:" + shortEvidenceHash(snapshot.work.ID+"\x00"+snapshot.plan.ID+"\x00"+snapshot.revision.SnapshotHash+"\x00"+status),
		PlanID:  snapshot.plan.ID, WorkSpecID: snapshot.work.ID, RevisionID: snapshot.revision.ID, SnapshotHash: snapshot.revision.SnapshotHash,
		Status: status, Criteria: criteria, CompletedAt: completedAt,
	}
}

func verificationRetryMessage(snapshot runtimeEvidenceSnapshot, missing []string) string {
	if len(missing) == 0 {
		missing = []string{"Record current criterion-linked verification evidence"}
	}
	return "Before answering, complete exactly one verification retry for the current workspace snapshot. Run or perform each missing check, inspect failures, and only then answer:\n- " + strings.Join(missing, "\n- ")
}

func guardrailTodoItems(todo session.TodoList, enforceSessionTodo bool) []session.TodoItem {
	if !enforceSessionTodo {
		return nil
	}
	return incompleteTodoItems(todo)
}

func incompleteTodoItems(todo session.TodoList) []session.TodoItem {
	items := make([]session.TodoItem, 0)
	for _, phase := range todo.Phases {
		for _, item := range phase.Items {
			if item.Status == session.TodoPending || item.Status == session.TodoInProgress {
				items = append(items, item)
			}
		}
	}
	return items
}

func unfinishedTodoRetryMessage(items []session.TodoItem) string {
	var builder strings.Builder
	builder.WriteString("[Host] This run still has unfinished Todo items. You may not finish until every item is completed or cancelled.\n")
	for _, item := range items {
		status := strings.TrimSpace(string(item.Status))
		if status == "" {
			status = string(session.TodoPending)
		}
		fmt.Fprintf(&builder, "- %s: %s\n", status, strings.TrimSpace(item.Content))
	}
	builder.WriteString("Continue the current in_progress item. Do not write a final answer yet.")
	return builder.String()
}

func appendUniqueSource(values []session.SourceRefV1, value session.SourceRefV1) []session.SourceRefV1 {
	for _, current := range values {
		if current.Kind == value.Kind && current.ID == value.ID && current.Range == value.Range && current.SHA256 == value.SHA256 {
			return values
		}
	}
	return append(values, value)
}

func shortEvidenceHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:12])
}
