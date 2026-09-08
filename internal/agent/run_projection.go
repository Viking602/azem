package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/Viking602/azem/internal/agentruntime"
)

type RunProjectionSource struct {
	Run     agentruntime.Run               `json:"run"`
	Task    agentruntime.Task              `json:"task"`
	Binding *agentruntime.ExecutionBinding `json:"binding,omitempty"`
}

func (s *Service) LoadRunProjection(ctx context.Context, runID string) (RunProjectionSource, error) {
	run, task, _, err := s.loadRunAggregate(ctx, strings.TrimSpace(runID))
	if err != nil {
		return RunProjectionSource{}, err
	}
	result := RunProjectionSource{Run: run, Task: task}
	agentID := strings.TrimSpace(run.Metadata[singleRunMetadataAgentID])
	if agentID == "" {
		agentID = mainAgentID
	}
	binding, err := s.store.LoadLatestExecutionBinding(ctx, run.ID, agentID, executionKind(agentID, run.Metadata))
	if err != nil {
		if errors.Is(err, agentruntime.ErrNotFound) {
			return result, nil
		}
		return RunProjectionSource{}, err
	}
	result.Binding = &binding
	// Reconnect/selection must not load execution blobs, attempts, or hashes.
	return result, nil
}

func (s *Service) QueueItemRuns(ctx context.Context) (map[string]string, error) {
	result := make(map[string]string)
	work, err := s.store.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer work.Rollback(context.Background())
	runs, err := work.Runs().ListRuns(ctx, agentruntime.RunSelector{})
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		if itemID := strings.TrimSpace(run.Metadata["queue_item_id"]); itemID != "" {
			result[strings.TrimSpace(run.Metadata["session_id"])+"\x00"+itemID] = run.ID
		}
	}
	if err := work.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
