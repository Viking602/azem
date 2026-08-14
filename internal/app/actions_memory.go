package app

import (
	"context"
	"fmt"

	"github.com/Viking602/azem/internal/memory"
)

var memoryActionHandlers = map[ActionKind]actionHandler{
	ActionListMemories: func(s *Service, ctx context.Context, action Action) error {
		if s.memory == nil {
			return fmt.Errorf("memory is unavailable")
		}
		items, err := s.memory.List(ctx, action.Target, 20)
		if err != nil {
			return err
		}
		s.emit(ctx, Event{Kind: EventMemoryState, State: "listed", Memories: items})
		return nil
	},
	ActionRemember: func(s *Service, ctx context.Context, action Action) error {
		if s.memory == nil {
			return fmt.Errorf("memory is unavailable")
		}
		item, err := s.memory.Remember(ctx, action.Target, action.SessionID, "manual", 50)
		if err != nil {
			return err
		}
		s.emit(ctx, Event{Kind: EventMemoryState, SessionID: action.SessionID, State: "remembered", Memories: []memory.Memory{item}})
		return nil
	},
	ActionForgetMemory: func(s *Service, ctx context.Context, action Action) error {
		if s.memory == nil {
			return fmt.Errorf("memory is unavailable")
		}
		if err := s.memory.Forget(ctx, action.Target); err != nil {
			return err
		}
		s.emit(ctx, Event{Kind: EventMemoryState, State: "forgotten", Text: action.Target})
		return nil
	},
	ActionShowRecap: func(s *Service, ctx context.Context, action Action) error {
		item, err := s.loadRecap(ctx, action.SessionID)
		if err != nil {
			return err
		}
		state := "loaded"
		if item == nil {
			state = "empty"
		}
		s.emit(ctx, Event{Kind: EventRecapState, SessionID: action.SessionID, State: state, Recap: item})
		return nil
	},
}
