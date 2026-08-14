package app

import (
	"context"
	"strings"

	"github.com/Viking602/azem/internal/session"
)

func (s *Service) UsageReport(ctx context.Context, scope string) (session.UsageReport, error) {
	query := session.UsageReportQuery{Scope: scope, Workspace: s.currentUsageWorkspace()}
	if s.sessions == nil {
		emptyScope := session.UsageScopeProject
		if strings.EqualFold(strings.TrimSpace(scope), session.UsageScopeAll) {
			emptyScope = session.UsageScopeAll
		}
		return session.UsageReport{Scope: emptyScope, Workspace: query.Workspace, Empty: true}, nil
	}
	return s.sessions.UsageReport(ctx, query)
}

func (s *Service) emitUsageReport(ctx context.Context, scope, state string) error {
	report, err := s.UsageReport(ctx, scope)
	if err != nil {
		return err
	}
	cloned := report.Clone()
	s.emit(ctx, Event{Kind: EventUsageReport, State: state, UsageReport: &cloned})
	return nil
}

func (s *Service) currentUsageWorkspace() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	workspace := strings.TrimSpace(s.workspaceAnchor)
	if workspace == "" {
		workspace = strings.TrimSpace(s.cfg.Workspace.Root)
	}
	if workspace == "" {
		return ""
	}
	return canonicalWorkspaceAnchor(workspace)
}
