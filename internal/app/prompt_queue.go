package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/session"
)

const (
	PromptQueueEnqueue = "enqueue"
	PromptQueueUpdate  = "update"
	PromptQueueRemove  = "remove"
	PromptQueueReorder = "reorder"
	PromptQueueRetry   = "retry"
	PromptQueuePause   = "pause"
	PromptQueueResume  = "resume"
	PromptQueueGuide   = "guide"
)

type PromptQueueMutation struct {
	Operation        string                 `json:"operation"`
	SessionID        string                 `json:"sessionId"`
	MutationID       string                 `json:"mutationId"`
	ExpectedRevision int64                  `json:"expectedRevision"`
	Item             session.QueuedPromptV1 `json:"item,omitempty"`
	ItemID           string                 `json:"itemId,omitempty"`
	BeforeID         string                 `json:"beforeId,omitempty"`
	PauseReason      string                 `json:"pauseReason,omitempty"`
	RunID            string                 `json:"runId,omitempty"`
}

var ErrInvalidPromptQueueAction = errors.New("invalid prompt queue action")

func (s *Service) PromptQueue(ctx context.Context, sessionID string) (session.PromptQueueV1, error) {
	if s.sessions == nil {
		return session.PromptQueueV1{}, errors.New("session store is unavailable")
	}
	return s.sessions.LoadPromptQueue(ctx, sessionID)
}

func (s *Service) MutatePromptQueue(ctx context.Context, mutation PromptQueueMutation) (session.PromptQueueV1, error) {
	if s.sessions == nil {
		return session.PromptQueueV1{}, errors.New("session store is unavailable")
	}
	mutation.SessionID = strings.TrimSpace(mutation.SessionID)
	mutation.MutationID = strings.TrimSpace(mutation.MutationID)
	if mutation.SessionID == "" || mutation.MutationID == "" || mutation.ExpectedRevision < 0 {
		return session.PromptQueueV1{}, fmt.Errorf("%w: session, mutation ID, and revision are required", ErrInvalidPromptQueueAction)
	}
	current, err := s.sessions.LoadPromptQueue(ctx, mutation.SessionID)
	if err != nil {
		return session.PromptQueueV1{}, err
	}
	if current.Revision != mutation.ExpectedRevision {
		return current, &session.PromptQueueRevisionConflictError{Expected: mutation.ExpectedRevision, Current: current}
	}
	next := current.Clone()
	now := time.Now().UTC()
	wake := false
	switch strings.TrimSpace(mutation.Operation) {
	case PromptQueueGuide:
		return s.guideQueuedPrompt(ctx, mutation, current)
	case PromptQueueEnqueue:
		item := mutation.Item
		item.ID = strings.TrimSpace(item.ID)
		item.Text = strings.TrimSpace(item.Text)
		if item.ID == "" || item.Text == "" {
			return current, fmt.Errorf("%w: queued item ID and text are required", ErrInvalidPromptQueueAction)
		}
		if index := promptQueueItemIndex(next.Items, item.ID); index >= 0 {
			existing := next.Items[index]
			if existing.Text == item.Text && equalAttachments(existing.Attachments, item.Attachments) {
				return current, nil
			}
			return current, fmt.Errorf("%w: queue item %q already exists", ErrInvalidPromptQueueAction, item.ID)
		}
		if err := s.validatePromptQueueAttachments(mutation.SessionID, item.Attachments); err != nil {
			return current, err
		}
		item.State, item.RunID, item.Error, item.Attempts = session.QueuedPromptQueued, "", "", 0
		item.CreatedAt, item.UpdatedAt = now, now
		next.Items = append(next.Items, item)
		wake = true
	case PromptQueueUpdate:
		itemID := firstNonempty(mutation.ItemID, mutation.Item.ID)
		index := promptQueueItemIndex(next.Items, itemID)
		if index < 0 {
			return current, fmt.Errorf("%w: queue item %q was not found", ErrInvalidPromptQueueAction, itemID)
		}
		if !editableQueuedPrompt(next.Items[index].State) {
			return current, fmt.Errorf("%w: dispatching queue item %q is immutable", ErrInvalidPromptQueueAction, itemID)
		}
		text := strings.TrimSpace(mutation.Item.Text)
		if text == "" {
			return current, fmt.Errorf("%w: updated queue text is required", ErrInvalidPromptQueueAction)
		}
		if err := s.validatePromptQueueAttachments(mutation.SessionID, mutation.Item.Attachments); err != nil {
			return current, err
		}
		next.Items[index].Text = text
		next.Items[index].Attachments = append([]session.Attachment(nil), mutation.Item.Attachments...)
		next.Items[index].UpdatedAt = now
	case PromptQueueRemove:
		index := promptQueueItemIndex(next.Items, mutation.ItemID)
		if index < 0 {
			return current, fmt.Errorf("%w: queue item %q was not found", ErrInvalidPromptQueueAction, mutation.ItemID)
		}
		if !editableQueuedPrompt(next.Items[index].State) {
			return current, fmt.Errorf("%w: dispatching queue item %q is immutable", ErrInvalidPromptQueueAction, mutation.ItemID)
		}
		next.Items = append(next.Items[:index], next.Items[index+1:]...)
	case PromptQueueReorder:
		index := promptQueueItemIndex(next.Items, mutation.ItemID)
		if index < 0 || !editableQueuedPrompt(next.Items[index].State) {
			return current, fmt.Errorf("%w: queue item %q cannot be reordered", ErrInvalidPromptQueueAction, mutation.ItemID)
		}
		if mutation.ItemID == mutation.BeforeID {
			return current, nil
		}
		item := next.Items[index]
		next.Items = append(next.Items[:index], next.Items[index+1:]...)
		before := len(next.Items)
		if mutation.BeforeID != "" {
			before = promptQueueItemIndex(next.Items, mutation.BeforeID)
			if before < 0 {
				return current, fmt.Errorf("%w: reorder target %q was not found", ErrInvalidPromptQueueAction, mutation.BeforeID)
			}
		}
		next.Items = append(next.Items, session.QueuedPromptV1{})
		copy(next.Items[before+1:], next.Items[before:])
		next.Items[before] = item
		next.Items[before].UpdatedAt = now
	case PromptQueueRetry:
		index := promptQueueItemIndex(next.Items, mutation.ItemID)
		if index < 0 || next.Items[index].State != session.QueuedPromptFailed {
			return current, fmt.Errorf("%w: queue item %q is not failed", ErrInvalidPromptQueueAction, mutation.ItemID)
		}
		next.Items[index].State = session.QueuedPromptQueued
		next.Items[index].RunID = ""
		next.Items[index].Error = ""
		next.Items[index].UpdatedAt = now
		wake = true
	case PromptQueuePause:
		next.State = session.PromptQueuePaused
		next.PauseReason = strings.TrimSpace(mutation.PauseReason)
	case PromptQueueResume:
		next.State = session.PromptQueueActive
		next.PauseReason = ""
		wake = true
	default:
		return current, fmt.Errorf("%w: unsupported operation %q", ErrInvalidPromptQueueAction, mutation.Operation)
	}
	saved, err := s.sessions.SavePromptQueueCAS(ctx, mutation.SessionID, mutation.ExpectedRevision, next)
	if err != nil {
		return saved, err
	}
	s.emitPromptQueueState(saved)
	if wake {
		s.StartPromptQueueCoordinator()
		s.wakePromptQueue()
	}
	return saved, nil
}

func (s *Service) guideQueuedPrompt(ctx context.Context, mutation PromptQueueMutation, current session.PromptQueueV1) (session.PromptQueueV1, error) {
	index := promptQueueItemIndex(current.Items, mutation.ItemID)
	if index < 0 || !editableQueuedPrompt(current.Items[index].State) {
		return current, fmt.Errorf("%w: queue item cannot be guided", ErrInvalidPromptQueueAction)
	}
	item := current.Items[index]
	if err := s.validatePromptQueueAttachments(mutation.SessionID, item.Attachments); err != nil {
		return current, err
	}
	s.mu.Lock()
	if s.shuttingDown || s.activeSession != mutation.SessionID || s.activeRun != mutation.RunID || mutation.RunID == "" {
		s.mu.Unlock()
		return current, ErrStaleRun
	}
	control := s.turnControls[mutation.RunID]
	if !s.guidanceOpen || control == nil {
		s.mu.Unlock()
		return current, ErrGuidanceClosed
	}
	next := current.Clone()
	next.Items = append(next.Items[:index], next.Items[index+1:]...)
	saved, sequence, err := s.sessions.SavePromptQueueWithGuidance(ctx, mutation.SessionID, mutation.ExpectedRevision, next, session.Block{
		Kind: "user", State: "guidance", Title: "Guidance", RunID: mutation.RunID, Content: item.Text, Attachments: item.Attachments,
	})
	if err == nil {
		err = control.Enqueue(turnControlMessage{ID: fmt.Sprintf("%s:%d", mutation.RunID, sequence), Kind: turnControlSteer, Message: UserMessageWithAttachments(item.Text, item.Attachments)})
	}
	s.mu.Unlock()
	if err != nil {
		return saved, err
	}
	s.emitPromptQueueState(saved)
	_ = s.emitSessionProjectionState(ctx, mutation.SessionID)
	return saved, nil
}

func (s *Service) StartPromptQueueCoordinator() {
	if s == nil || s.sessions == nil {
		return
	}
	s.mu.Lock()
	shuttingDown := s.shuttingDown
	s.mu.Unlock()
	if shuttingDown {
		return
	}
	s.promptQueueOnce.Do(func() {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.runPromptQueueCoordinator()
		}()
	})
	s.wakePromptQueue()
}

func (s *Service) runPromptQueueCoordinator() {
	_ = s.reconcilePromptQueues(s.ctx)
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.promptQueueWake:
			for s.dispatchNextQueuedPrompt(s.ctx) {
			}
		}
	}
}

func (s *Service) dispatchNextQueuedPrompt(ctx context.Context) bool {
	s.mu.Lock()
	busy := s.shuttingDown || s.activeRun != ""
	s.mu.Unlock()
	if busy {
		return false
	}
	queues, err := s.sessions.ListPromptQueues(ctx)
	if err != nil {
		return false
	}
	type candidate struct {
		queue session.PromptQueueV1
		item  session.QueuedPromptV1
	}
	candidates := make([]candidate, 0)
	for _, queue := range queues {
		if queue.State != session.PromptQueueActive || len(queue.Items) == 0 || queue.Items[0].State != session.QueuedPromptQueued {
			continue
		}
		candidates = append(candidates, candidate{queue: queue, item: queue.Items[0]})
	}
	if len(candidates) == 0 {
		return false
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].item.CreatedAt.Equal(candidates[right].item.CreatedAt) {
			if candidates[left].queue.SessionID == candidates[right].queue.SessionID {
				return candidates[left].item.ID < candidates[right].item.ID
			}
			return candidates[left].queue.SessionID < candidates[right].queue.SessionID
		}
		return candidates[left].item.CreatedAt.Before(candidates[right].item.CreatedAt)
	})
	selected := candidates[0]
	claimed := selected.queue.Clone()
	claimed.Items[0].State = session.QueuedPromptDispatching
	claimed.Items[0].Attempts++
	claimed.Items[0].UpdatedAt = time.Now().UTC()
	claimedQueue, err := s.sessions.SavePromptQueueCAS(ctx, claimed.SessionID, claimed.Revision, claimed)
	if err != nil {
		return false
	}
	s.emitPromptQueueState(claimedQueue)
	owner, err := s.sessions.LoadSession(ctx, claimed.SessionID)
	if err != nil {
		s.failDispatchedPrompt(ctx, claimed.SessionID, selected.item.ID, "load queued session: "+err.Error())
		return true
	}
	runID, err := s.StartConfiguredTurn(TurnRequest{
		SessionID: claimed.SessionID, Prompt: selected.item.Text, Images: append([]session.Attachment(nil), selected.item.Attachments...),
		Provider: owner.ProviderID, Model: owner.ModelID, Reasoning: owner.Reasoning, AgentMode: owner.AgentMode,
		queueItemID: selected.item.ID,
	})
	if err != nil {
		if errors.Is(err, ErrRunActive) {
			s.resetDispatchedPrompt(ctx, claimed.SessionID, selected.item.ID)
			return false
		}
		s.failDispatchedPrompt(ctx, claimed.SessionID, selected.item.ID, err.Error())
		return true
	}
	s.consumeDispatchedPrompt(ctx, claimed.SessionID, selected.item.ID, runID)
	return false
}

func (s *Service) consumeDispatchedPrompt(ctx context.Context, sessionID, itemID, runID string) {
	for range 4 {
		queue, err := s.sessions.LoadPromptQueue(ctx, sessionID)
		if err != nil {
			return
		}
		index := promptQueueItemIndex(queue.Items, itemID)
		if index < 0 {
			return
		}
		if queue.Items[index].State != session.QueuedPromptDispatching {
			return
		}
		queue.Items[index].RunID = runID
		queue.Items = append(queue.Items[:index], queue.Items[index+1:]...)
		saved, err := s.sessions.SavePromptQueueCAS(ctx, sessionID, queue.Revision, queue)
		if errors.Is(err, session.ErrPromptQueueRevisionConflict) {
			continue
		}
		if err == nil {
			s.emitPromptQueueState(saved)
		}
		return
	}
}

func (s *Service) resetDispatchedPrompt(ctx context.Context, sessionID, itemID string) {
	s.updateDispatchedPrompt(ctx, sessionID, itemID, func(item *session.QueuedPromptV1) {
		item.State, item.RunID, item.Error = session.QueuedPromptQueued, "", ""
	})
}

func (s *Service) failDispatchedPrompt(ctx context.Context, sessionID, itemID, reason string) {
	s.updateDispatchedPrompt(ctx, sessionID, itemID, func(item *session.QueuedPromptV1) {
		item.State, item.RunID, item.Error = session.QueuedPromptFailed, "", boundedProjectionText(reason)
	})
}

func (s *Service) updateDispatchedPrompt(ctx context.Context, sessionID, itemID string, update func(*session.QueuedPromptV1)) {
	for range 4 {
		queue, err := s.sessions.LoadPromptQueue(ctx, sessionID)
		if err != nil {
			return
		}
		index := promptQueueItemIndex(queue.Items, itemID)
		if index < 0 || queue.Items[index].State != session.QueuedPromptDispatching {
			return
		}
		update(&queue.Items[index])
		queue.Items[index].UpdatedAt = time.Now().UTC()
		saved, err := s.sessions.SavePromptQueueCAS(ctx, sessionID, queue.Revision, queue)
		if errors.Is(err, session.ErrPromptQueueRevisionConflict) {
			continue
		}
		if err == nil {
			s.emitPromptQueueState(saved)
		}
		return
	}
}

func (s *Service) reconcilePromptQueues(ctx context.Context) error {
	durableConsumed := map[string]string{}
	if s.coding != nil {
		var err error
		durableConsumed, err = s.coding.QueueItemRuns(ctx)
		if err != nil {
			return err
		}
	}
	queues, err := s.sessions.ListPromptQueues(ctx)
	if err != nil {
		return err
	}
	for _, queue := range queues {
		projection, projectionErr := s.sessions.LoadDisplayProjection(ctx, queue.SessionID)
		if projectionErr != nil && !errors.Is(projectionErr, session.ErrSessionNotFound) {
			return projectionErr
		}
		consumed := make(map[string]struct{})
		for _, block := range projection.Blocks {
			if itemID := block.Data["queueItemId"]; itemID != "" {
				consumed[itemID] = struct{}{}
			}
		}
		for _, item := range queue.Items {
			if _, exists := durableConsumed[queue.SessionID+"\x00"+item.ID]; exists {
				consumed[item.ID] = struct{}{}
			}
		}
		changed := false
		items := queue.Items[:0]
		for _, item := range queue.Items {
			if item.State != session.QueuedPromptDispatching {
				items = append(items, item)
				continue
			}
			if _, exists := consumed[item.ID]; exists {
				changed = true
				continue
			}
			item.State, item.RunID, item.Error = session.QueuedPromptQueued, "", ""
			item.UpdatedAt = time.Now().UTC()
			items = append(items, item)
			changed = true
		}
		if !changed {
			continue
		}
		queue.Items = append([]session.QueuedPromptV1(nil), items...)
		saved, err := s.sessions.SavePromptQueueCAS(ctx, queue.SessionID, queue.Revision, queue)
		if err != nil {
			if errors.Is(err, session.ErrPromptQueueRevisionConflict) {
				continue
			}
			return err
		}
		s.emitPromptQueueState(saved)
	}
	return nil
}

func (s *Service) pausePromptQueue(ctx context.Context, sessionID, reason string) {
	if s == nil || s.sessions == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	for range 4 {
		queue, err := s.sessions.LoadPromptQueue(ctx, sessionID)
		if err != nil || len(queue.Items) == 0 || queue.State == session.PromptQueuePaused {
			return
		}
		queue.State = session.PromptQueuePaused
		queue.PauseReason = reason
		saved, err := s.sessions.SavePromptQueueCAS(ctx, sessionID, queue.Revision, queue)
		if errors.Is(err, session.ErrPromptQueueRevisionConflict) {
			continue
		}
		if err == nil {
			s.emitPromptQueueState(saved)
		}
		return
	}
}

func (s *Service) emitPromptQueueState(queue session.PromptQueueV1) {
	copy := queue.Clone()
	_ = s.events.Publish(Event{
		Kind: EventPromptQueueState, SessionID: queue.SessionID, State: string(queue.State),
		PromptQueue: &copy, At: time.Now().UTC(),
	})
}

func (s *Service) wakePromptQueue() {
	if s == nil || s.promptQueueWake == nil {
		return
	}
	select {
	case s.promptQueueWake <- struct{}{}:
	default:
	}
}

func (s *Service) validatePromptQueueAttachments(sessionID string, attachments []session.Attachment) error {
	for _, attachment := range attachments {
		if _, err := s.ReadImageAttachment(sessionID, attachment); err != nil {
			return fmt.Errorf("queue attachment %q is not owned by session %s: %w", attachment.ID, sessionID, err)
		}
	}
	return nil
}

func promptQueueItemIndex(items []session.QueuedPromptV1, itemID string) int {
	itemID = strings.TrimSpace(itemID)
	for index := range items {
		if items[index].ID == itemID {
			return index
		}
	}
	return -1
}

func editableQueuedPrompt(state session.QueuedPromptState) bool {
	return state == session.QueuedPromptQueued || state == session.QueuedPromptFailed
}

func equalAttachments(left, right []session.Attachment) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
