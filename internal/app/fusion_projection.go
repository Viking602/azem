package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/session"
)

// Fusion owns a persistent execution context, but presents one conversation.
// Keep control IDs, usage attribution and the two model histories intact.
type fusionHost struct {
	providerHost
	parentRunID string
	model       string
}

func (h fusionHost) EmitEvent(ctx context.Context, event Event) bool {
	switch event.Kind {
	case EventAgentState, EventProviderRetry:
		return ctx.Err() == nil
	case EventTextDelta, EventThinkingDelta, EventToolStarted, EventToolUpdate, EventToolFinished, EventDiffReady:
		event.Data = cloneProjectionData(event.Data)
		event.Data["fusionRole"] = "sidekick"
		event.Data["sourceLabel"] = "Sidekick · " + h.model
		event.Data["executionRunId"] = event.RunID
		event.Data["executionToolCallId"] = event.ToolCallID
		if event.Kind == EventTextDelta || event.Kind == EventThinkingDelta {
			event.Data["fusionBlockId"] = fusionToolID(event.RunID, event.Data["transcriptBlockId"])
			// A Sidekick report must never settle the lead turn as its final answer.
			event.TextPhase = "commentary"
		} else {
			event.ToolCallID = fusionToolID(event.RunID, event.ToolCallID)
		}
		event.RunID, event.AgentID = h.parentRunID, ""
	}
	return h.providerHost.EmitEvent(ctx, event)
}

func fusionToolID(runID, callID string) string {
	return "fusion:" + runID + ":" + callID
}

// Build both desktop snapshot formats from the same projection. Identify
// Fusion by its actual parent call, never the selected mode or worker name.
func (s *Service) projectFusionSession(ctx context.Context, projection *session.Projection) ([]AgentSnapshotPayload, error) {
	handoffs := make(map[string]session.ToolRecord)
	for _, record := range projection.ToolRecords {
		if record.Name == "sidekick" {
			handoffs[record.RunID+"\x00"+record.ToolCallID] = record
		}
	}
	var snapshots []agentservice.SubagentSnapshot
	if s.providers != nil {
		snapshots = s.providers.ListSubagents(ctx, projection.Session.ID)
	}
	agents := make([]AgentSnapshotPayload, 0, len(snapshots))
	children := make(map[string]session.ToolRecord)
	hiddenAgents := make(map[string]bool)
	for _, snapshot := range snapshots {
		if !snapshot.Found {
			continue
		}
		run := snapshot.Run
		if handoff, ok := handoffs[run.ParentRunID+"\x00"+run.ParentToolCallID]; ok {
			children[run.ChildRunID] = handoff
			hiddenAgents[run.ID] = true
			continue
		}
		event := subagentStateEvent(run, run.Summary)
		agents = append(agents, AgentSnapshotPayload{ID: run.ID, State: string(run.State), Summary: run.Summary, Agent: *event.Agent})
	}
	if len(handoffs) == 0 {
		return agents, nil
	}
	projection.Blocks = slices.DeleteFunc(slices.Clone(projection.Blocks), func(block session.Block) bool {
		_, handoff := handoffs[block.RunID+"\x00"+block.ParentToolCallID]
		return block.Kind == "agent" && (hiddenAgents[block.AgentID] || handoff)
	})
	// Older runs predate durable child tool records. Recover the actual calls
	// from the retained transcript without replaying inherited calls twice.
	records := slices.Clone(projection.ToolRecords)
	durableRuns := make(map[string]bool)
	seen := make(map[string]bool)
	inheritedCalls := make(map[string]bool)
	for _, record := range records {
		durableRuns[record.RunID] = true
		seen[record.RunID+"\x00"+record.ToolCallID] = true
		if _, child := children[record.RunID]; child {
			inheritedCalls[record.ToolCallID] = true
		}
	}
	for _, snapshot := range snapshots {
		run := snapshot.Run
		if !hiddenAgents[run.ID] || durableRuns[run.ChildRunID] {
			continue
		}
		messages, err := agentruntime.UnmarshalMessages(run.Transcript)
		if len(run.Transcript) == 0 {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("restore Fusion tools: %w", err)
		}
		pending := make(map[string]int)
		for _, item := range messages {
			if internalSubagentTranscriptMessage(item) {
				continue
			}
			messageRun := firstNonempty(agentruntime.MessageRunID(item), run.ChildRunID)
			if messageRun != run.ChildRunID {
				continue
			}
			for _, call := range item.ToolCalls {
				key := messageRun + "\x00" + call.ID
				if seen[key] || (agentruntime.MessageRunID(item) == "" && inheritedCalls[call.ID]) {
					continue
				}
				seen[key] = true
				inheritedCalls[call.ID] = true
				pending[call.ID] = len(records)
				records = append(records, session.ToolRecord{SessionID: run.SessionID, RunID: messageRun, ToolCallID: call.ID,
					Name: call.Name, Arguments: call.Arguments, State: session.ToolInterrupted, StartedAt: run.StartedAt})
			}
			if result := item.ToolResult; result != nil {
				if index, ok := pending[result.ToolCallID]; ok {
					record := &records[index]
					record.State, record.Content = session.ToolCompleted, boundedUTF8(result.Content, maxToolRecordPreviewBytes)
					if result.IsError {
						record.State = session.ToolFailed
					}
					if len(result.Structured) <= maxInlineToolRecordBytes {
						record.Structured = result.Structured
					}
					record.CompletedAt = run.FinishedAt
				}
			}
		}
	}
	projection.ToolRecords = make([]session.ToolRecord, 0, len(records))
	for _, record := range records {
		if handoff, ok := children[record.RunID]; ok {
			record.ToolCallID = fusionToolID(record.RunID, record.ToolCallID)
			record.RunID, record.AnchorSequence = handoff.RunID, handoff.AnchorSequence
		}
		projection.ToolRecords = append(projection.ToolRecords, record)
	}
	slices.SortStableFunc(projection.ToolRecords, func(a, b session.ToolRecord) int {
		return a.StartedAt.Compare(b.StartedAt)
	})
	for _, snapshot := range snapshots {
		run := snapshot.Run
		if !hiddenAgents[run.ID] {
			continue
		}
		blocks, err := s.providers.DetailSubagent(ctx, run.SessionID, run.ID)
		if err != nil {
			return nil, fmt.Errorf("restore Fusion conversation: %w", err)
		}
		handoff := children[run.ChildRunID]
		projection.Blocks = append(projection.Blocks, fusionConversationBlocks(run, handoff, blocks)...)
	}
	return agents, nil
}

// Display-only blocks retain their durable anchor. Place each piece of prose
// after the preceding actual call; never renumber persisted conversation blocks.
func fusionConversationBlocks(run agentservice.SubagentRun, handoff session.ToolRecord, blocks []AgentTranscriptBlock) []session.Block {
	var input struct {
		Prompt string `json:"prompt"`
	}
	_ = json.Unmarshal(handoff.Arguments, &input)
	prompt := boundedUTF8(strings.TrimSpace(input.Prompt), maxAgentTranscriptTextBytes)
	for index := len(blocks) - 1; index >= 0; index-- {
		if blocks[index].Kind == "user" && blocks[index].Content == prompt {
			blocks = blocks[index+1:]
			break
		}
	}
	result := make([]session.Block, 0, len(blocks))
	after := handoff.ToolCallID
	for _, block := range blocks {
		if block.RunID != "" && block.RunID != run.ChildRunID {
			continue
		}
		kind := "assistant"
		switch block.Kind {
		case "tool":
			after = fusionToolID(run.ChildRunID, block.ToolCallID)
			continue
		case "thinking":
			kind = "thinking"
		case "commentary", "assistant":
		default:
			continue
		}
		result = append(result, session.Block{
			Sequence: handoff.AnchorSequence, RunID: handoff.RunID, Kind: kind,
			Content: block.Content, State: block.State, TextPhase: "commentary",
			Data: map[string]string{"fusionRole": "sidekick", "sourceLabel": "Sidekick · " + run.Model,
				"executionRunId": run.ChildRunID, "fusionBlockId": fusionToolID(run.ChildRunID, block.ID), "fusionAfterToolCallId": after},
		})
	}
	return result
}
