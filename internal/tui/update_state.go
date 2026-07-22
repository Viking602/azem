package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/i18n"
	"github.com/Viking602/azem/internal/session"
)

func (m *AppModel) updateAgent(event app.Event) {
	if event.AgentID == "" || event.Agent == nil {
		return
	}
	value := agentViewFromPayload(event.AgentID, event.State, event.Text, event.Agent)
	for index := range m.agents {
		if m.agents[index].ID == event.AgentID {
			value.Blocks = m.agents[index].Blocks
			m.agents[index] = value
			m.updateAgentBlock(event, value)
			return
		}
	}
	m.agents = append(m.agents, value)
	m.updateAgentBlock(event, value)
}

func (m *AppModel) updateAgentBlock(event app.Event, agent AgentView) {
	content := first(agent.Activity, agent.Summary, agent.Description)
	for index := range m.transcript {
		block := &m.transcript[index]
		if block.Kind == BlockAgent && block.ID == agent.ID {
			block.Title = first(agent.Role, "Subagent")
			block.Content = content
			block.State = agent.State
			return
		}
	}
	m.transcript = append(m.transcript, Block{
		ID: agent.ID, Kind: BlockAgent, RunID: first(agent.ParentRunID, event.RunID),
		ToolCallID: agent.ParentToolCallID, Title: first(agent.Role, "Subagent"),
		Content: content, State: agent.State,
	})
}

func agentViewFromPayload(id, state, summary string, payload *app.AgentStatePayload) AgentView {
	if payload == nil {
		return AgentView{ID: id, State: state, Summary: summary}
	}
	return AgentView{
		ID: id, Role: payload.Type, State: state, Summary: summary, Description: payload.Description,
		Model: payload.Model, Background: payload.Background, CapabilityMode: payload.CapabilityMode,
		RequestedIsolation: payload.RequestedIsolation, Isolation: payload.Isolation, CWD: payload.CWD,
		ParentRunID: payload.ParentRunID, ParentToolCallID: payload.ParentToolCallID,
		ChildRunID: payload.ChildRunID, Activity: payload.Activity, Warning: payload.Warning,
		WorktreePath: payload.WorktreePath, ToolCalls: payload.ToolCalls, Turns: payload.Turns,
		TokensUsed: payload.TokensUsed, ElapsedMS: payload.ElapsedMS,
	}
}

func (m *AppModel) updateAgentStream(event app.Event) {
	index := -1
	for candidate := range m.agents {
		if m.agents[candidate].ID == event.AgentID {
			index = candidate
			break
		}
	}
	if index < 0 {
		m.agents = append(m.agents, AgentView{ID: event.AgentID, Role: "Subagent", State: "running"})
		index = len(m.agents) - 1
	}
	agent := &m.agents[index]
	switch event.Kind {
	case app.EventThinkingDelta:
		agent.Activity = first(compactAgentActivity(event.Text), "thinking")
		appendAgentViewDelta(&agent.Blocks, BlockThinking, event.RunID, "Thinking", event.Text)
	case app.EventTextDelta:
		agent.Activity = "responding"
		appendAgentViewDelta(&agent.Blocks, BlockAssistant, event.RunID, "Assistant", event.Text)
	case app.EventToolStarted:
		agent.Activity = first(event.Data["name"], event.Text, "tool")
		upsertAgentTool(&agent.Blocks, event, "running", m.catalog)
	case app.EventToolUpdate:
		agent.Activity = first(event.Data["name"], event.Text, "tool update")
		upsertAgentTool(&agent.Blocks, event, "running", m.catalog)
	case app.EventToolFinished:
		agent.Activity = first(event.Data["name"], "tool finished")
		upsertAgentTool(&agent.Blocks, event, terminalToolState(event.State), m.catalog)
	case app.EventHookStarted, app.EventHookFinished, app.EventHookDiagnostic:
		m.updateHooks(&agent.Blocks, event)

	}
}

func (m *AppModel) hasRunningHooks() bool {
	for _, block := range m.transcript {
		for _, hook := range block.Hooks {
			if hook.State == "running" {
				return true
			}
		}
	}
	for _, agent := range m.agents {
		for _, block := range agent.Blocks {
			for _, hook := range block.Hooks {
				if hook.State == "running" {
					return true
				}
			}
		}
	}
	return false
}

func (m AppModel) hasRunningAgents() bool {
	for _, agent := range m.agents {
		switch strings.ToLower(agent.State) {
		case "initializing", "running", "cancelling":
			return true
		}
	}
	return false
}

func (m *AppModel) updateHooks(blocks *[]Block, event app.Event) {
	hook := hookRunFromEvent(event)
	if event.Kind == app.EventHookDiagnostic {
		*blocks = append(*blocks, Block{ID: event.ToolCallID, Kind: BlockHook, RunID: event.RunID, Title: hook.Event, State: "failed", Hooks: []HookRunView{hook}})
	} else if event.Kind == app.EventHookFinished {
		for i := len(*blocks) - 1; i >= 0; i-- {
			if (*blocks)[i].Kind == BlockHook && upsertMatchingHook(&(*blocks)[i].Hooks, hook) {
				if hook.State == "completed" {
					*blocks = append((*blocks)[:i], (*blocks)[i+1:]...)
					m.invalidateTranscriptLayout()
					return
				}
				(*blocks)[i].State = hook.State
				m.invalidateTranscriptLayout()
				return
			}
		}
		if hook.State != "completed" {
			*blocks = append(*blocks, Block{Kind: BlockHook, RunID: event.RunID, Title: hook.Event, State: hook.State, Hooks: []HookRunView{hook}})
		}
	} else {
		*blocks = append(*blocks, Block{Kind: BlockHook, RunID: event.RunID, Title: hook.Event, State: "running", Hooks: []HookRunView{hook}})
	}
	m.invalidateTranscriptLayout()
}

func (m *AppModel) invalidateTranscriptLayout() {
	if m.transcriptLayout != nil {
		m.transcriptLayout.initialized = false
	}
}

func hookRunFromEvent(event app.Event) HookRunView {
	d := event.Data
	h := HookRunView{Event: d["event"], Name: first(d["name"], "hook"), Source: filepath.Base(d["source"]), State: "running"}
	h.Key = h.Event + "\x00" + h.Name + "\x00" + h.Source
	h.DurationMS, _ = strconv.ParseInt(d["durationMS"], 10, 64)
	h.Truncated, _ = strconv.ParseBool(d["stdoutTruncated"])
	stderrTruncated, _ := strconv.ParseBool(d["stderrTruncated"])
	h.Truncated = h.Truncated || stderrTruncated
	if event.Kind == app.EventHookDiagnostic {
		h.State = "failed"
		h.Reason = compactHookText(first(d["reason"], event.Text), 3, 2048)
		return h
	}
	if event.Kind == app.EventHookFinished {
		h.State = first(event.State, d["state"], "completed")
		if h.State != "blocked" && h.State != "failed" && d["exitCode"] != "" && d["exitCode"] != "0" {
			h.State = "failed"
		}
		h.Reason = compactHookText(d["reason"], 3, 2048)
		stdout := strings.TrimSpace(d["stdout"])
		if strings.HasPrefix(stdout, "{") {
			stdout = ""
		}
		if h.State == "failed" || h.State == "blocked" {
			h.Output = compactHookText(first(h.Reason, d["stderr"], stdout), 3, 2048)
		} else {
			h.Output = compactHookText(stdout, 3, 2048)
		}
	}
	return h
}

func compactHookText(value string, lines, bytes int) string {
	parts := strings.Split(strings.TrimSpace(value), "\n")
	if len(parts) > lines {
		parts = parts[:lines]
	}
	value = strings.Join(parts, "\n")
	if len(value) > bytes {
		value = string([]byte(value)[:bytes])
		value = strings.ToValidUTF8(value, "") + "…"
	}
	return value
}

func upsertMatchingHook(hooks *[]HookRunView, hook HookRunView) bool {
	for i := len(*hooks) - 1; i >= 0; i-- {
		if (*hooks)[i].Key == hook.Key && (*hooks)[i].State == "running" {
			(*hooks)[i] = hook
			return true
		}
	}
	return false
}

func compactAgentActivity(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 80 {
		value = string(runes[:79]) + "…"
	}
	return value
}

func appendAgentViewDelta(blocks *[]Block, kind BlockKind, runID, title, content string) {
	if content == "" {
		return
	}
	if len(*blocks) > 0 {
		last := &(*blocks)[len(*blocks)-1]
		if last.Kind == kind && last.RunID == runID && !toolStateTerminal(last.State) {
			last.Content = appendStreamContent(last.Content, content, kind)
			return
		}
	}
	*blocks = append(*blocks, Block{
		ID: fmt.Sprintf("live-%s-%d", kind, len(*blocks)), Kind: kind, RunID: runID,
		Title: title, Content: content, State: "streaming",
	})
}

func upsertAgentTool(blocks *[]Block, event app.Event, state string, catalog i18n.Catalog) {
	for index := len(*blocks) - 1; index >= 0; index-- {
		block := &(*blocks)[index]
		if (block.Kind != BlockTool && block.Kind != BlockDiff) || block.ToolCallID != event.ToolCallID {
			continue
		}
		if event.Kind == app.EventToolFinished || !toolStateTerminal(block.State) {
			block.State = state
			if event.Kind == app.EventToolFinished && state == "completed" {
				if title, diff, ok := summarizeFileChange(block.Title, block.Arguments, event.Data["structured"], event.Text); ok {
					block.Kind, block.Title, block.Content = BlockDiff, title, diff
				} else {
					block.Content = summarizeToolResult(block.Title, block.Arguments, event.Text, catalog)
				}
			} else if event.Kind == app.EventToolFinished {
				block.Content = joinToolSummary(summarizeToolArguments(block.Title, block.Arguments), summarizeToolFailure(block.Title, event.Text))
			} else {
				appendBlockContent(block, event.Text)
			}
		}
		return
	}
	name := first(event.Data["name"], "Tool")
	content := first(event.Data["arguments"], event.Text)
	kind := BlockTool
	title := name
	if event.Kind == app.EventToolFinished && state == "completed" {
		if diffTitle, diff, ok := summarizeFileChange(name, event.Data["arguments"], event.Data["structured"], event.Text); ok {
			kind, title, content = BlockDiff, diffTitle, diff
		}
	}
	*blocks = append(*blocks, Block{
		ID: event.ToolCallID, Kind: kind, RunID: event.RunID, ToolCallID: event.ToolCallID,
		Title: title, Arguments: event.Data["arguments"], Content: content, State: state,
	})
}

func (m *AppModel) updateAgentDetail(event app.Event) {
	switch event.State {
	case "detail":
		for index := range m.agents {
			if m.agents[index].ID == event.AgentID {
				m.agents[index].Blocks = agentTranscriptBlocks(event.AgentBlocks, m.catalog)
				m.detailAgentID = event.AgentID
				m.openOverlay(OverlayAgentDetail)
				return
			}
		}
	case "agent_types":
		m.agentTypes = agentCatalogViews(event.AgentCatalog)
		m.openOverlay(OverlayAgentTypes)
	case "personas":
		m.personas = agentCatalogViews(event.AgentCatalog)
		m.openOverlay(OverlayPersonas)
	case "cancel_requested", "already_finished":
		m.errorBanner = event.Text
	}
}

func agentTranscriptBlocks(blocks []app.AgentTranscriptBlock, catalogs ...i18n.Catalog) []Block {
	catalog := i18n.Must(i18n.DefaultLanguage)
	if len(catalogs) > 0 {
		catalog = catalogs[0]
	}
	result := make([]Block, 0, len(blocks))
	for _, block := range blocks {
		kind := BlockKind(block.Kind)
		content, arguments := block.Content, ""
		if block.Kind == string(BlockTool) {
			arguments, content = splitAgentToolContent(block.Content)
			switch block.State {
			case "completed":
				if title, diff, ok := summarizeFileChange(block.Title, arguments, "", content); ok {
					kind, block.Title, content = BlockDiff, title, diff
				} else {
					content = summarizeToolResult(block.Title, arguments, content, catalog)
				}
			case "failed", "cancelled":
				content = joinToolSummary(summarizeToolArguments(block.Title, arguments), summarizeToolFailure(block.Title, content))
			default:
				content = summarizeToolArguments(block.Title, arguments)
			}
		}
		result = append(result, Block{
			ID: block.ID, Kind: kind, RunID: block.RunID, ToolCallID: block.ToolCallID,
			Title: block.Title, Arguments: arguments, Content: content, State: block.State,
		})
	}
	return result
}

func splitAgentToolContent(content string) (string, string) {
	arguments, rest, found := strings.Cut(content, "\n")
	if !found || !json.Valid([]byte(arguments)) {
		return "", content
	}
	var object map[string]any
	if json.Unmarshal([]byte(arguments), &object) != nil {
		return "", content
	}
	return arguments, rest
}

func agentCatalogViews(entries []app.AgentCatalogEntry) []AgentCatalogView {
	result := make([]AgentCatalogView, 0, len(entries))
	for _, entry := range entries {
		result = append(result, AgentCatalogView{
			Name: entry.Name, Description: entry.Description, Persona: entry.Persona, Model: entry.Model,
			Reasoning: entry.Reasoning, CapabilityMode: entry.CapabilityMode, Isolation: entry.Isolation,
			Source: entry.Source, Enabled: entry.Enabled,
		})
	}
	return result
}

func (m *AppModel) updateMCP(event app.Event) {
	if encoded := event.Data["servers"]; encoded != "" {
		if json.Unmarshal([]byte(encoded), &m.mcpServers) == nil {
			sort.Slice(m.mcpServers, func(i, j int) bool { return m.mcpServers[i].Name < m.mcpServers[j].Name })
		}
		return
	}
	name := first(event.Data["server"], event.AgentID)
	if name == "" {
		return
	}
	toolCount, _ := strconv.Atoi(event.Data["toolCount"])
	value := MCPView{Name: name, State: first(event.State, event.Data["state"]), ToolCount: toolCount, Error: first(event.Text, event.Data["error"])}
	for index := range m.mcpServers {
		if m.mcpServers[index].Name == name {
			m.mcpServers[index] = value
			return
		}
	}
	m.mcpServers = append(m.mcpServers, value)
	sort.Slice(m.mcpServers, func(i, j int) bool { return m.mcpServers[i].Name < m.mcpServers[j].Name })
}

func (m *AppModel) updateAuth(event app.Event) {
	provider := first(event.Data["provider"], event.AgentID)
	if provider == "" {
		return
	}
	m.auth[provider] = AuthView{
		Provider: provider, AccountID: event.Data["accountID"], Email: event.Data["email"],
		DisplayName: event.Data["displayName"], Plan: event.Data["plan"], State: first(event.State, event.Data["state"]),
	}
}

func (m *AppModel) loadModels(event app.Event) {
	provider := first(event.Data["provider"], m.provider)
	var choices []ModelChoice
	if err := json.Unmarshal([]byte(event.Data["models"]), &choices); err != nil {
		m.errorBanner = "decode model catalog: " + err.Error()
		return
	}
	sort.Slice(choices, func(i, j int) bool { return choices[i].ID < choices[j].ID })
	if m.modelsByProvider == nil {
		m.modelsByProvider = make(map[string][]ModelChoice)
	}
	m.modelsByProvider[provider] = choices
	if provider == m.provider {
		m.selectModels(choices)
	}
}

func (m *AppModel) switchProvider(provider string) {
	if provider == "" {
		return
	}
	if provider != m.provider {
		m.usage.InputTokens = 0
		m.usage.CacheInputTokens = 0
		m.usage.CachedInputTokens = 0
		m.usage.MainCacheInput = 0
		m.usage.MainCachedInput = 0
		m.usage.CacheWriteTokens = 0
		m.usage.MainCacheWrite = 0
		m.usage.OutputTokens = 0
		m.usage.ReasoningTokens = 0
		m.usage.UncachedInputTokens = 0
		m.usage.CompactionInput = 0
		m.usage.CompactionCached = 0
		m.usage.CompactionCacheWrite = 0
		m.usage.CompactionOutput = 0
		m.usage.CompactionReasoning = 0
		m.usage.CompactionUncached = 0
		m.usage.TeamInput = 0
		m.usage.TeamCached = 0
		m.usage.TeamCacheWrite = 0
		m.usage.TeamOutput = 0
		m.usage.TeamReasoning = 0
		m.usage.TeamUncached = 0
		m.usage.CacheReported = false
		m.usage.MainCacheReported = false
	}
	m.provider = provider
	m.selectModels(m.modelsByProvider[provider])
}

func (m *AppModel) selectModels(choices []ModelChoice) {
	if m.modelsByProvider == nil {
		m.modelsByProvider = make(map[string][]ModelChoice)
	}
	m.modelsByProvider[m.provider] = choices
	m.models = choices
	for _, choice := range choices {
		if choice.ID == m.model {
			m.selectModel(m.model)
			return
		}
	}
	if len(choices) == 0 {
		m.selectModel("")
		return
	}
	m.selectModel(choices[0].ID)
}

func (m *AppModel) selectModel(modelID string) {
	if modelID != m.model {
		m.usage.InputTokens = 0
		m.usage.CacheInputTokens = 0
		m.usage.CachedInputTokens = 0
		m.usage.MainCacheInput = 0
		m.usage.MainCachedInput = 0
		m.usage.CacheWriteTokens = 0
		m.usage.MainCacheWrite = 0
		m.usage.OutputTokens = 0
		m.usage.ReasoningTokens = 0
		m.usage.UncachedInputTokens = 0
		m.usage.CompactionInput = 0
		m.usage.CompactionCached = 0
		m.usage.CompactionCacheWrite = 0
		m.usage.CompactionOutput = 0
		m.usage.CompactionReasoning = 0
		m.usage.CompactionUncached = 0
		m.usage.TeamInput = 0
		m.usage.TeamCached = 0
		m.usage.TeamCacheWrite = 0
		m.usage.TeamOutput = 0
		m.usage.TeamReasoning = 0
		m.usage.TeamUncached = 0
		m.usage.CacheReported = false
		m.usage.MainCacheReported = false
	}
	m.model = modelID
	m.usage.ContextLimit = 0
	for _, choice := range m.models {
		if choice.ID == modelID {
			m.usage.ContextLimit = choice.ContextWindow
			break
		}
	}
	m.syncReasoningForModel()
}

func (m *AppModel) updateUsage(data map[string]string) {
	if data == nil {
		return
	}
	if data["factSnapshot"] == "true" && data["usageSnapshot"] != "" {
		m.restoreUsage(data["usageSnapshot"])
		return
	}
	requestKind := data["requestKind"]
	inputTokens, inputErr := strconv.Atoi(data["inputTokens"])
	if inputErr == nil && data["inputTokens"] != "" {
		if data["aggregateOnly"] != "true" {
			m.usage.InputTokens = inputTokens
			if data["cacheStatus"] == "reported" {
				m.usage.MainCacheInput += inputTokens
			}
		}
		if data["cacheStatus"] == "reported" {
			m.usage.CacheInputTokens += inputTokens
		}
		if requestKind == "compaction" {
			m.usage.CompactionInput += inputTokens
		} else if requestKind == "team" && data["cacheStatus"] == "reported" {
			m.usage.TeamInput += inputTokens
		}
	}
	if value, err := strconv.Atoi(data["cachedInputTokens"]); err == nil && data["cachedInputTokens"] != "" {
		m.usage.CachedInputTokens += value
		m.usage.CacheReported = true
		if data["aggregateOnly"] != "true" {
			m.usage.MainCachedInput += value
			m.usage.MainCacheReported = true
		}
		if requestKind == "compaction" {
			m.usage.CompactionCached += value
		} else if requestKind == "team" {
			m.usage.TeamCached += value
		}
	}
	if value, err := strconv.Atoi(data["cacheWriteTokens"]); err == nil && data["cacheWriteTokens"] != "" {
		m.usage.CacheWriteTokens += value
		if requestKind == "main" {
			m.usage.MainCacheWrite += value
		}
		if requestKind == "compaction" {
			m.usage.CompactionCacheWrite += value
		} else if requestKind == "team" {
			m.usage.TeamCacheWrite += value
		}
	}
	if value, err := strconv.Atoi(data["outputTokens"]); err == nil && data["outputTokens"] != "" {
		if data["aggregateOnly"] != "true" {
			m.usage.OutputTokens = value
		}
		if requestKind == "compaction" {
			m.usage.CompactionOutput += value
		} else if requestKind == "team" {
			m.usage.TeamOutput += value
		}
	}
	if value, err := strconv.Atoi(data["reasoningTokens"]); err == nil && data["reasoningTokens"] != "" {
		if requestKind == "compaction" {
			m.usage.CompactionReasoning += value
		} else if requestKind == "main" {
			m.usage.ReasoningTokens = value
		} else if requestKind == "team" {
			m.usage.TeamReasoning += value
		}
	}
	if value, err := strconv.Atoi(data["uncachedInputTokens"]); err == nil && data["uncachedInputTokens"] != "" {
		if requestKind == "compaction" {
			m.usage.CompactionUncached += value
		} else if requestKind == "main" {
			m.usage.UncachedInputTokens = value
		} else if requestKind == "team" {
			m.usage.TeamUncached += value
		}
	}
	if value, err := strconv.Atoi(data["contextLimit"]); err == nil && data["contextLimit"] != "" {
		m.usage.ContextLimit = value
	}
	if requestKind != "" {
		m.usage.LastRequestKind = requestKind
	}
	if data["provider"] != "" {
		m.usage.LastProvider = data["provider"]
	}
	if data["model"] != "" {
		m.usage.LastModel = data["model"]
	}
	if data["transport"] != "" {
		m.usage.LastTransport = data["transport"]
	}
}

func (m *AppModel) resetTurnUsage() {
	m.usage.InputTokens = 0
	m.usage.CacheInputTokens = 0
	m.usage.CachedInputTokens = 0
	m.usage.MainCacheInput = 0
	m.usage.MainCachedInput = 0
	m.usage.CacheWriteTokens = 0
	m.usage.MainCacheWrite = 0
	m.usage.OutputTokens = 0
	m.usage.ReasoningTokens = 0
	m.usage.UncachedInputTokens = 0
	m.usage.CompactionInput = 0
	m.usage.CompactionCached = 0
	m.usage.CompactionCacheWrite = 0
	m.usage.CompactionOutput = 0
	m.usage.CompactionReasoning = 0
	m.usage.CompactionUncached = 0
	m.usage.TeamInput = 0
	m.usage.TeamCached = 0
	m.usage.TeamCacheWrite = 0
	m.usage.TeamOutput = 0
	m.usage.TeamReasoning = 0
	m.usage.TeamUncached = 0
	m.usage.CacheReported = false
	m.usage.MainCacheReported = false
}

func (m *AppModel) restoreUsage(raw string) {
	usage, err := session.DecodeUsage([]byte(raw))
	if err != nil || usage.IsZero() {
		return
	}
	if usage.ContextLimit > 0 {
		m.usage.ContextLimit = usage.ContextLimit
	}
	m.usage.CurrentCacheEpoch = usage.CurrentCacheEpoch
	m.usage.CurrentEpochMainInput = usage.CurrentEpochMainInput
	m.usage.CurrentEpochMainReportedInput = usage.CurrentEpochMainReportedInput
	m.usage.CurrentEpochMainCached = usage.CurrentEpochMainCached
	m.usage.CurrentEpochMainRequests = usage.CurrentEpochMainRequests
	m.usage.CurrentEpochMainReportedRequests = usage.CurrentEpochMainReportedRequests
	m.usage.LifetimeMainInput = usage.LifetimeMainInput
	m.usage.LifetimeMainReportedInput = usage.LifetimeMainReportedInput
	m.usage.LifetimeMainCached = usage.LifetimeMainCached
	m.usage.LifetimeMainOutput = usage.LifetimeMainOutput
	m.usage.LifetimeMainRequests = usage.LifetimeMainRequests
	m.usage.CompactionReportedInput = usage.CompactionReportedInput
	m.usage.CompactionRequests = usage.CompactionRequests
	m.usage.CompactionReportedRequests = usage.CompactionReportedRequests
	m.usage.CompactionCacheReported = usage.CompactionCacheReported
	m.usage.TeamRequests = usage.TeamRequests
	m.usage.SubagentInput = usage.SubagentInput
	m.usage.SubagentRequests = usage.SubagentRequests
	m.usage.InputTokens = usage.InputTokens
	m.usage.OutputTokens = usage.OutputTokens
	m.usage.CacheInputTokens = usage.CacheInputTokens
	m.usage.CachedInputTokens = usage.CachedInputTokens
	m.usage.MainCacheInput = usage.MainCacheInput
	m.usage.MainCachedInput = usage.MainCachedInput
	m.usage.CacheWriteTokens = usage.CacheWriteTokens
	m.usage.MainCacheWrite = usage.MainCacheWrite
	m.usage.ReasoningTokens = usage.ReasoningTokens
	m.usage.UncachedInputTokens = usage.UncachedInputTokens
	m.usage.CompactionInput = usage.CompactionInput
	m.usage.CompactionCached = usage.CompactionCached
	m.usage.CompactionCacheWrite = usage.CompactionCacheWrite
	m.usage.CompactionOutput = usage.CompactionOutput
	m.usage.CompactionReasoning = usage.CompactionReasoning
	m.usage.CompactionUncached = usage.CompactionUncached
	m.usage.TeamInput = usage.TeamInput
	m.usage.TeamCached = usage.TeamCached
	m.usage.TeamCacheWrite = usage.TeamCacheWrite
	m.usage.TeamOutput = usage.TeamOutput
	m.usage.TeamReasoning = usage.TeamReasoning
	m.usage.TeamUncached = usage.TeamUncached
	m.usage.CacheReported = usage.CacheReported
	m.usage.MainCacheReported = usage.MainCacheReported
	m.usage.LastRequestKind = usage.LastRequestKind
	m.usage.LastProvider = usage.LastProvider
	m.usage.LastModel = usage.LastModel
	m.usage.LastTransport = usage.LastTransport
}

func (m AppModel) isRunning() bool {
	return m.status == "Starting" || m.status == "Running" || m.status == "Awaiting approval" || m.status == "Reviewing approval" || m.status == "Cancelling" || m.status == "Compacting"
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
