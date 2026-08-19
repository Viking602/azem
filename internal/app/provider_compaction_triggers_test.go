package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/api"
	"github.com/Viking602/venat/message"
)

type guidanceContextStub struct {
	compactTo func([]message.Message, int) ([]message.Message, error)
}

func (s guidanceContextStub) Build(context.Context, api.Task) ([]message.Message, error) {
	return nil, nil
}

func (s guidanceContextStub) Compact(_ context.Context, history []message.Message) ([]message.Message, error) {
	return s.CompactTo(context.Background(), history, 0)
}

func (s guidanceContextStub) CompactTo(_ context.Context, history []message.Message, target int) ([]message.Message, error) {
	return s.compactTo(history, target)
}

func TestActiveGuidanceIsIncludedAndAcknowledgedByArchive(t *testing.T) {
	snapshot := activeGuidanceSnapshot{values: []activeGuidanceMessage{{Text: "first correction"}, {Text: "second correction"}}}
	acknowledged := false
	manager := activeGuidanceContext{
		inner: guidanceContextStub{compactTo: func(history []message.Message, _ int) ([]message.Message, error) {
			return history, nil
		}},
		peek: func() activeGuidanceSnapshot { return snapshot },
		acknowledge: func(got activeGuidanceSnapshot) {
			acknowledged = len(got.values) == 2
		},
	}
	result, err := manager.CompactTo(context.Background(), []message.Message{message.NewText(message.RoleUser, "original")}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !acknowledged || len(result) != 2 ||
		!strings.Contains(result[1].Text, "1. first correction") ||
		!strings.Contains(result[1].Text, "2. second correction") {
		t.Fatalf("archive guidance result=%#v acknowledged=%v", result, acknowledged)
	}
}

func TestActiveGuidanceRetriesStaleArchiveWithLatestSnapshot(t *testing.T) {
	first := activeGuidanceSnapshot{values: []activeGuidanceMessage{{Text: "first correction", Sequence: 9}}}
	second := activeGuidanceSnapshot{values: []activeGuidanceMessage{{Text: "second correction", Sequence: 10}}}
	peekCalls, compactCalls := 0, 0
	manager := activeGuidanceContext{
		inner: guidanceContextStub{compactTo: func(history []message.Message, _ int) ([]message.Message, error) {
			compactCalls++
			if compactCalls == 1 {
				return history, session.ErrRunCheckpointStale
			}
			return history, nil
		}},
		peek: func() activeGuidanceSnapshot {
			peekCalls++
			if peekCalls == 1 {
				return first
			}
			return second
		},
		acknowledge: func(got activeGuidanceSnapshot) {
			if len(got.values) != 1 || got.values[0].Sequence != second.values[0].Sequence {
				t.Fatalf("acknowledged stale snapshot: %#v", got)
			}
		},
	}
	result, err := manager.CompactTo(context.Background(), nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if compactCalls != 2 || len(result) != 1 || result[0].Text != "second correction" {
		t.Fatalf("retry calls=%d result=%#v", compactCalls, result)
	}
}

func TestActiveGuidanceRemainsPendingWhenArchiveFails(t *testing.T) {
	acknowledged := false
	manager := activeGuidanceContext{
		inner: guidanceContextStub{compactTo: func(history []message.Message, _ int) ([]message.Message, error) {
			return history, errors.New("archive failed")
		}},
		peek: func() activeGuidanceSnapshot {
			return activeGuidanceSnapshot{values: []activeGuidanceMessage{{Text: "keep me"}}}
		},
		acknowledge: func(activeGuidanceSnapshot) { acknowledged = true },
	}
	if _, err := manager.CompactTo(context.Background(), nil, 100); err == nil {
		t.Fatal("expected archive failure")
	}
	if acknowledged {
		t.Fatal("failed archive acknowledged pending guidance")
	}
}
