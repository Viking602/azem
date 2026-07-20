package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/go-hydaelyn/api"
	"github.com/Viking602/go-hydaelyn/message"
	hyprovider "github.com/Viking602/go-hydaelyn/provider"
)

type subagentTurnContext struct {
	instructions   string
	privateContext string
	seed           []message.Message
	compactHooks   func(context.Context, []message.Message, []message.Message, error) error
	summarize      func(context.Context, string) (string, error)
}

func (c subagentTurnContext) Build(_ context.Context, task api.Task) ([]message.Message, error) {
	messages := make([]message.Message, 0, len(c.seed)+2)
	if instructions := strings.TrimSpace(c.instructions); instructions != "" {
		messages = append(messages, message.NewText(message.RoleSystem, instructions))
	}
	if privateContext := strings.TrimSpace(c.privateContext); privateContext != "" {
		value := message.NewText(message.RoleSystem, "[Trusted SubagentStart hook context]\n"+privateContext)
		value.Visibility = message.VisibilityPrivate
		messages = append(messages, value)
	}
	for _, seeded := range c.seed {
		if seeded.Role != message.RoleSystem {
			messages = append(messages, seeded)
		}
	}
	if goal := strings.TrimSpace(task.Goal); goal != "" {
		messages = append(messages, message.NewText(message.RoleUser, goal))
	}
	return messages, nil
}

func (c subagentTurnContext) Compact(ctx context.Context, history []message.Message) ([]message.Message, error) {
	if c.compactHooks != nil {
		if err := c.compactHooks(ctx, history, nil, nil); err != nil {
			return history, err
		}
	}
	const recentMessages = 20
	prefixEnd := 0
	for prefixEnd < len(history) && history[prefixEnd].Role == message.RoleSystem {
		prefixEnd++
	}
	if len(history) <= recentMessages+prefixEnd {
		if c.compactHooks != nil {
			_ = c.compactHooks(ctx, history, history, nil)
		}
		return history, nil
	}
	start := len(history) - recentMessages
	if start < prefixEnd {
		start = prefixEnd
	}
	for start > prefixEnd && history[start].Role != message.RoleUser {
		start--
	}
	compacted := make([]message.Message, 0, len(history)-start+prefixEnd)
	compacted = append(compacted, history[:prefixEnd]...)
	compacted = append(compacted, history[start:]...)
	if c.compactHooks != nil {
		_ = c.compactHooks(ctx, history, compacted, nil)
	}
	return compacted, nil
}

func (c subagentTurnContext) CompactTo(ctx context.Context, history []message.Message, targetTokens int) ([]message.Message, error) {
	return (turnContext{compactHooks: c.compactHooks, summarize: c.summarize}).CompactTo(ctx, history, targetTokens)
}

func effectiveSubagentTools(roleTools []string, capability string) map[string]bool {
	readOnly := map[string]bool{"coding.list_files": true, "coding.read_file": true, "coding.search": true, "coding.git_diff": true}
	modes := map[string]map[string]bool{
		"read-only":  readOnly,
		"read-write": {"coding.list_files": true, "coding.read_file": true, "coding.search": true, "coding.git_diff": true, "coding.edit_hashline": true, "coding.write_file": true, "coding.gofmt": true},
		"execute":    {"coding.list_files": true, "coding.read_file": true, "coding.search": true, "coding.git_diff": true, "coding.go_test": true, "coding.shell": true},
		"all":        {"coding.list_files": true, "coding.read_file": true, "coding.search": true, "coding.git_diff": true, "coding.edit_hashline": true, "coding.write_file": true, "coding.gofmt": true, "coding.go_test": true, "coding.shell": true},
	}
	allowed := make(map[string]bool)
	for _, name := range roleTools {
		if modes[capability][name] {
			allowed[name] = true
		}
	}
	return allowed
}

func renderSubagentInstructions(profile effectiveSubagentProfile) string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "You are the %s subagent.", profile.Type)
	if profile.Persona != "" {
		fmt.Fprintf(&rendered, " Apply the %s persona.", profile.Persona)
	}
	fmt.Fprintf(&rendered, " Work only within %s.\n", profile.CWD)
	if profile.Instructions != "" {
		rendered.WriteString(profile.Instructions)
		rendered.WriteByte('\n')
	}
	writeContract := func(title string, items []config.SubagentContractItem) {
		if len(items) == 0 {
			return
		}
		rendered.WriteString(title)
		rendered.WriteString(" contract:\n")
		for _, item := range items {
			requirement := "optional"
			if item.Required {
				requirement = "required"
			}
			fmt.Fprintf(&rendered, "- %s (%s, %s)", item.Name, item.Type, requirement)
			if item.Description != "" {
				fmt.Fprintf(&rendered, ": %s", item.Description)
			}
			rendered.WriteByte('\n')
		}
	}
	writeContract("Input", profile.Inputs)
	writeContract("Output", profile.Outputs)
	rendered.WriteString("Return a direct final answer with concrete evidence.")
	return strings.TrimSpace(rendered.String())
}

func transcriptToAgentBlocks(encoded json.RawMessage) ([]AgentTranscriptBlock, error) {
	if len(encoded) == 0 {
		return nil, nil
	}
	var messages []message.Message
	if err := json.Unmarshal(encoded, &messages); err != nil {
		return nil, fmt.Errorf("decode subagent transcript: %w", err)
	}
	blocks := make([]AgentTranscriptBlock, 0, len(messages))
	callIndex := make(map[string]int)
	for index, item := range messages {
		if item.Role == message.RoleSystem {
			continue
		}
		if item.Role == message.RoleUser && strings.TrimSpace(item.Text) != "" {
			blocks = append(blocks, AgentTranscriptBlock{ID: fmt.Sprintf("msg-%d-user", index), Kind: "user", RunID: item.RunID, Content: item.Text, State: "completed"})
		}
		if item.Role == message.RoleAssistant {
			if item.Thinking != "" {
				blocks = append(blocks, AgentTranscriptBlock{ID: fmt.Sprintf("msg-%d-thinking", index), Kind: "thinking", RunID: item.RunID, Content: item.Thinking, State: "completed"})
			}
			if item.Text != "" {
				blocks = append(blocks, AgentTranscriptBlock{ID: fmt.Sprintf("msg-%d-text", index), Kind: "assistant", RunID: item.RunID, Content: item.Text, State: "completed"})
			}
			for _, call := range item.ToolCalls {
				callIndex[call.ID] = len(blocks)
				blocks = append(blocks, AgentTranscriptBlock{ID: "call-" + call.ID, Kind: "tool", RunID: item.RunID, ToolCallID: call.ID, Title: call.Name, Content: string(call.Arguments), State: "running"})
			}
		}
		if item.ToolResult != nil {
			if blockIndex, ok := callIndex[item.ToolResult.ToolCallID]; ok {
				appendAgentBlockContent(&blocks[blockIndex], item.ToolResult.Content)
				if item.ToolResult.IsError {
					blocks[blockIndex].State = "failed"
				} else {
					blocks[blockIndex].State = "completed"
				}
				delete(callIndex, item.ToolResult.ToolCallID)
			} else {
				blocks = append(blocks, AgentTranscriptBlock{
					ID: fmt.Sprintf("result-%d", index), Kind: "tool", RunID: item.RunID,
					ToolCallID: item.ToolResult.ToolCallID, Title: item.ToolResult.Name,
					Content: item.ToolResult.Content, State: "failed",
				})
			}
		}
	}
	for _, index := range callIndex {
		blocks[index].State = "failed"
		appendAgentBlockContent(&blocks[index], "missing tool result")
	}
	return blocks, nil
}

func appendAgentDelta(blocks *[]AgentTranscriptBlock, kind, runID, title, content string) {
	if content == "" {
		return
	}
	if len(*blocks) > 0 {
		last := &(*blocks)[len(*blocks)-1]
		if last.Kind == kind && last.RunID == runID {
			last.Content += content
			return
		}
	}
	*blocks = append(*blocks, AgentTranscriptBlock{ID: fmt.Sprintf("live-%s-%d", kind, len(*blocks)), Kind: kind, RunID: runID, Title: title, Content: content, State: "streaming"})
}

func finishAgentToolBlock(blocks []AgentTranscriptBlock, callID, content string, failed bool) {
	for index := len(blocks) - 1; index >= 0; index-- {
		if blocks[index].Kind == "tool" && blocks[index].ToolCallID == callID {
			appendAgentBlockContent(&blocks[index], content)
			if failed {
				blocks[index].State = "failed"
			} else {
				blocks[index].State = "completed"
			}
			return
		}
	}
}

func appendAgentBlockContent(block *AgentTranscriptBlock, content string) {
	if content == "" {
		return
	}
	if block.Content != "" && !strings.HasSuffix(block.Content, "\n") {
		block.Content += "\n"
	}
	block.Content += content
}

func snapshotFromRun(run agentservice.SubagentRun) agentservice.SubagentSnapshot {
	elapsed := time.Duration(0)
	if !run.StartedAt.IsZero() {
		end := run.FinishedAt
		if end.IsZero() {
			end = time.Now().UTC()
		}
		elapsed = max(0, end.Sub(run.StartedAt))
	}
	return agentservice.SubagentSnapshot{Run: cloneSubagentRun(run), Elapsed: elapsed, Found: true}
}

func cloneSubagentRun(run agentservice.SubagentRun) agentservice.SubagentRun {
	run.Transcript = append(json.RawMessage(nil), run.Transcript...)
	run.ToolsUsed = append([]string(nil), run.ToolsUsed...)
	return run
}

func sortedToolSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func subagentTerminal(state agentservice.SubagentState) bool {
	switch state {
	case agentservice.SubagentCompleted, agentservice.SubagentFailed, agentservice.SubagentCancelled, agentservice.SubagentInterrupted:
		return true
	default:
		return false
	}
}

func subagentSummary(run agentservice.SubagentRun) string {
	value := firstNonempty(run.Output, run.Error, string(run.State))
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 240 {
		value = string(runes[:240])
	}
	return value
}

func appendWarning(current, warning string) string {
	if current == "" {
		return warning
	}
	if warning == "" || strings.Contains(current, warning) {
		return current
	}
	return current + "; " + warning
}

func compactActivity(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 120 {
		return string(runes[:120])
	}
	return value
}

func newSubagentID() (string, error) {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate subagent ID: %w", err)
	}
	return "subagent_" + hex.EncodeToString(value), nil
}

func providerHasModel(driver hyprovider.Driver, model string) bool {
	for _, candidate := range driver.Metadata().Models {
		if candidate == model {
			return true
		}
	}
	return false
}

func sortedRoleNames(roles map[string]config.SubagentRoleConfig, toggles map[string]bool) []string {
	names := make([]string, 0, len(roles))
	for name := range roles {
		if enabled, configured := toggles[name]; !configured || enabled {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func resolveSubagentCWD(workspaceRoot, requested string) (string, error) {
	requested = strings.Trim(strings.TrimSpace(requested), "`\"")
	if requested == "" {
		return "", fmt.Errorf("cwd is empty")
	}
	if requested == "~" || strings.HasPrefix(requested, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve cwd home: %w", err)
		}
		requested = filepath.Join(home, strings.TrimPrefix(requested, "~/"))
	}
	if !filepath.IsAbs(requested) {
		requested = filepath.Join(workspaceRoot, requested)
	}
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root symlinks: %w", err)
	}
	resolved, err := filepath.Abs(filepath.Clean(requested))
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve cwd symlinks: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect cwd: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd %q is not a directory", resolved)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("cwd %q escapes parent workspace %q", resolved, root)
	}
	return resolved, nil
}

// Driver protocol.
