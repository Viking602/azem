package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/Viking602/azem/internal/config"
)

type planYoloHandoff struct {
	SessionID string
	PlanID    string
	Title     string
	Target    config.ModelRouteConfig
}

func (s *Service) RegisterPlanYoloHandoff(runID, sessionID, planID, title string, target config.ModelRouteConfig) error {
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(planID) == "" || strings.TrimSpace(target.Provider) == "" || strings.TrimSpace(target.Model) == "" {
		return fmt.Errorf("plan-yolo handoff is incomplete")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingPlanYolo == nil {
		s.pendingPlanYolo = make(map[string]planYoloHandoff)
	}
	if _, exists := s.pendingPlanYolo[runID]; exists {
		return fmt.Errorf("plan-yolo handoff for run %q is already registered", runID)
	}
	s.pendingPlanYolo[runID] = planYoloHandoff{SessionID: sessionID, PlanID: planID, Title: strings.TrimSpace(title), Target: target}
	return nil
}

func (s *Service) startPlanYoloHandoff(parentRunID string) bool {
	s.mu.Lock()
	handoff, exists := s.pendingPlanYolo[parentRunID]
	if exists {
		delete(s.pendingPlanYolo, parentRunID)
	}
	s.mu.Unlock()
	if !exists {
		return false
	}
	reasoning := handoff.Target.Reasoning
	if reasoning == "" && s.sessions != nil {
		if current, err := s.sessions.LoadSession(context.Background(), handoff.SessionID); err == nil {
			reasoning = current.Reasoning
		}
	}
	runID, err := s.StartConfiguredTurn(TurnRequest{
		SessionID: handoff.SessionID,
		Prompt:    fmt.Sprintf("Plan approved automatically: %s. Read the approved plan, implement it exactly, then re-read it and verify every step before completion.", handoff.Title),
		Provider:  handoff.Target.Provider, Model: handoff.Target.Model, Reasoning: reasoning, AgentMode: "single",
		PlanMode: false, DisableSubagents: false, approvedPlanArtifactID: handoff.PlanID,
	})
	if err != nil {
		s.emit(s.ctx, Event{Kind: EventPlanResolved, SessionID: handoff.SessionID, RunID: parentRunID, PlanID: handoff.PlanID, State: "handoff_failed", Text: err.Error(), Data: map[string]string{"title": handoff.Title}})
		return false
	}
	s.emit(s.ctx, Event{Kind: EventPlanResolved, SessionID: handoff.SessionID, RunID: runID, PlanID: handoff.PlanID, State: "executing", Data: map[string]string{"title": handoff.Title, "automatic": "true", "parentRunId": parentRunID}})
	return true
}
