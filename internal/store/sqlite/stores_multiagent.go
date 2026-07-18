package sqlite

import (
	"context"
	"sort"

	"github.com/Viking602/go-hydaelyn/api"
)

func (u *unitOfWork) SaveHandoff(ctx context.Context, value api.HandoffRecord) error {
	return u.save(ctx, kindHandoff, value.ID, value.RunID, value.RunID, "", "", value.CreatedAt, "", "", value, false)
}

func (u *unitOfWork) LoadHandoff(ctx context.Context, runID string, handoffID string) (api.HandoffRecord, error) {
	return loadRecord[api.HandoffRecord](ctx, u.tx, kindHandoff, handoffID, runID)
}

func (u *unitOfWork) ListHandoffs(ctx context.Context, selector api.HandoffSelector) ([]api.HandoffRecord, error) {
	values, err := listRecords[api.HandoffRecord](ctx, u.tx, kindHandoff, selector.RunID)
	if err != nil {
		return nil, err
	}
	filtered := values[:0]
	for _, value := range values {
		if selector.From != "" && value.From != selector.From || selector.To != "" && value.To != selector.To || !selector.Since.IsZero() && value.CreatedAt.Before(selector.Since) {
			continue
		}
		filtered = append(filtered, value)
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].ID < filtered[j].ID })
	return filtered, nil
}

func (u *unitOfWork) SaveTeamState(ctx context.Context, value api.TeamStateRecord) error {
	return u.save(ctx, kindTeamState, value.RunID, "", value.RunID, "", "", value.UpdatedAt, "", "", value, true)
}

func (u *unitOfWork) LoadTeamState(ctx context.Context, runID string) (api.TeamStateRecord, error) {
	return loadRecord[api.TeamStateRecord](ctx, u.tx, kindTeamState, runID, "")
}

func (u *unitOfWork) SaveAgentInstance(ctx context.Context, value api.AgentInstanceRecord) error {
	return u.save(ctx, kindInstance, value.ID, "", value.RunID, value.TaskID, value.State, value.CreatedAt, "", "", value, true)
}

func (u *unitOfWork) LoadAgentInstance(ctx context.Context, id string) (api.AgentInstanceRecord, error) {
	return loadRecord[api.AgentInstanceRecord](ctx, u.tx, kindInstance, id, "")
}

func (u *unitOfWork) ListAgentInstances(ctx context.Context, selector api.AgentInstanceSelector) ([]api.AgentInstanceRecord, error) {
	values, err := listRecords[api.AgentInstanceRecord](ctx, u.tx, kindInstance, selector.RunID)
	if err != nil {
		return nil, err
	}
	filtered := values[:0]
	for _, value := range values {
		if selector.ClassName != "" && value.ClassName != selector.ClassName || selector.State != "" && value.State != selector.State {
			continue
		}
		filtered = append(filtered, value)
	}
	return filtered, nil
}
