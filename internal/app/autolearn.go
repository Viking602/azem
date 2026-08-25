package app

import (
	"context"
	"errors"
	"time"
)

const autoLearnCapturePrompt = `Review only the just-completed turn for genuinely reusable procedures. If a repeatable setup sequence, debugging recipe, or project workflow would help in future sessions, create or update one isolated managed skill with manage_skill. Prefer updating a close existing managed skill over creating a duplicate. If nothing is worth keeping, call no tool and stop. Do not resume the prior task or produce a user-facing answer.`

func (s *Service) maybeScheduleAutoLearn(request TurnRequest, toolCalls int) {
	s.mu.Lock()
	autoLearn := s.cfg.AutoLearn
	sessions := s.sessions
	s.mu.Unlock()
	if !autoLearn.Enabled || !autoLearn.AutoContinue || request.origin == turnOriginAutoLearn ||
		request.PlanMode || request.VibeMode || request.AgentMode != "single" || toolCalls < autoLearn.MinToolCalls || sessions == nil {
		return
	}
	goalCtx, cancelGoal := context.WithTimeout(context.Background(), time.Second)
	activeGoal, err := activeGoalContext(goalCtx, sessions, request.SessionID)
	cancelGoal()
	if err != nil || activeGoal != "" {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for attempt := 0; attempt < 50; attempt++ {
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			_, startErr := s.StartConfiguredTurn(TurnRequest{
				SessionID: request.SessionID, Prompt: autoLearnCapturePrompt,
				Provider: request.Provider, Model: request.Model, Reasoning: request.Reasoning,
				AgentMode: "single", DisableSubagents: true, origin: turnOriginAutoLearn,
			})
			if startErr == nil {
				return
			}
			if !errors.Is(startErr, ErrRunActive) {
				return
			}
		}
	}()
}
