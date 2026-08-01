package app

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Viking602/azem/internal/session"

	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	hyskill "github.com/Viking602/venat/skill"
	"github.com/Viking602/venat/tool"
)

const skillContextMetadataKey = "hydaelyn.skill.context"

type contextProfileProviderDriver struct {
	inner hyprovider.Driver
	emit  func(ContextProfile)
}

func (d *contextProfileProviderDriver) Metadata() hyprovider.Metadata {
	return d.inner.Metadata()
}

func (d *contextProfileProviderDriver) Stream(ctx context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	if d.emit != nil {
		d.emit(contextProfileFromRequest(request))
	}
	return d.inner.Stream(ctx, request)
}

func contextProfileFromRequest(request hyprovider.Request) ContextProfile {
	profile := ContextProfile{Source: "request", Estimated: true}
	systemIndex := 0
	messageIndex := map[message.Role]int{}
	for _, current := range request.Messages {
		if skillKind := current.Metadata[skillContextMetadataKey]; skillKind != "" {
			profile.Contributions = append(profile.Contributions, skillMessageContributions(current.Text, skillKind)...)
			continue
		}
		if current.Role == message.RoleSystem {
			systemIndex++
			name := "azem.core_instructions"
			if systemIndex > 1 {
				name = fmt.Sprintf("system:%d", systemIndex)
			}
			profile.Contributions = appendTokenContribution(profile.Contributions, ContextCategoryCore, name, estimateContextTokens([]message.Message{current}))
			continue
		}
		messageIndex[current.Role]++
		name := fmt.Sprintf("message:%s:%d", current.Role, messageIndex[current.Role])
		if current.ToolResult != nil {
			name = "tool_result:" + current.ToolResult.Name
		} else if current.Kind == message.KindCompactionSummary {
			name = fmt.Sprintf("compaction:%d", messageIndex[current.Role])
		}
		profile.Contributions = appendTokenContribution(profile.Contributions, ContextCategoryConversation, name, estimateContextTokens([]message.Message{current}))
	}
	for _, definition := range request.Tools {
		category := ContextCategoryBuiltinTools
		if strings.HasPrefix(definition.Origin, "mcp:") || strings.HasPrefix(definition.Name, "mcp__") {
			category = ContextCategoryMCP
		}
		profile.Contributions = appendTokenContribution(profile.Contributions, category, definition.Name, estimateToolTokens(definition))
	}
	return profile
}

func (r *ProviderRuntime) EstimateContextProfile(ctx context.Context, sessionID string) (ContextProfile, error) {
	profile := ContextProfile{Source: "bootstrap", Estimated: true}
	profile.Contributions = appendTokenContribution(profile.Contributions, ContextCategoryCore, "azem.core_instructions", estimateTextTokens(mainInstructions))

	skillSnapshot := r.coding.SkillSnapshot()
	if skillSnapshot.Registry != nil {
		active, err := skillSnapshot.Registry.Resolve(skillSnapshot.Eager...)
		if err != nil {
			return ContextProfile{}, err
		}
		if section := hyskill.RenderSystemSection(active); section != "" {
			profile.Contributions = append(profile.Contributions, skillMessageContributions(section, "active")...)
		}
		available := make([]hyskill.Skill, 0, len(skillSnapshot.Available))
		for _, name := range skillSnapshot.Available {
			resolved, ok := skillSnapshot.Registry.Get(name)
			if !ok {
				continue
			}
			available = append(available, resolved)
			profile.Contributions = appendTokenContribution(profile.Contributions, ContextCategorySkills, resolved.Name, estimateTextTokens("- "+resolved.Name+": "+resolved.Description))
		}
		if len(skillSnapshot.Available) > 0 {
			profile.Contributions = appendTokenContribution(profile.Contributions, ContextCategorySkills, "catalog.overhead", estimateTextTokens("Available Hydaelyn skills:\nWhen a task matches a description, activate the skill before proceeding."))
		}
		for _, definition := range skillRuntimeToolDefinitions(active, available) {
			profile.Contributions = appendToolContribution(profile.Contributions, ContextCategoryBuiltinTools, definition)
		}
	}

	workspaceDrivers, err := r.coding.WorkspaceDrivers(ctx, r.cfg.Workspace.Root)
	if err != nil {
		return ContextProfile{}, err
	}
	for _, driver := range workspaceDrivers {
		profile.Contributions = appendToolContribution(profile.Contributions, ContextCategoryBuiltinTools, driver.Definition())
	}

	r.mu.RLock()
	host, manager, subagents := r.host, r.mcp, r.subagents
	r.mu.RUnlock()
	if host != nil && host.sessions != nil {
		profile.Contributions = appendToolContribution(profile.Contributions, ContextCategoryBuiltinTools, (&todoDriver{}).Definition())
		profile.Contributions = appendToolContribution(profile.Contributions, ContextCategoryBuiltinTools, (&contextArtifactDriver{}).Definition())
	}
	if subagents != nil && r.cfg.Agents.Subagents.Enabled {
		profile.Contributions = appendToolContribution(profile.Contributions, ContextCategoryBuiltinTools, (&subagentSpawnDriver{runtime: subagents}).Definition())
		profile.Contributions = appendToolContribution(profile.Contributions, ContextCategoryBuiltinTools, (&subagentGetOutputDriver{}).Definition())
		profile.Contributions = appendToolContribution(profile.Contributions, ContextCategoryBuiltinTools, (&subagentKillDriver{}).Definition())
	}
	if manager != nil {
		for _, driver := range manager.Snapshot() {
			profile.Contributions = appendToolContribution(profile.Contributions, ContextCategoryMCP, driver.Definition())
		}
	}
	if host != nil && host.sessions != nil && strings.TrimSpace(sessionID) != "" {
		projection, loadErr := host.sessions.LoadProjection(ctx, sessionID)
		if loadErr == nil {
			profile.Contributions = append(profile.Contributions, conversationContributions(projectionContextMessages(projection))...)
		}
	}
	return profile, nil
}

func projectionContextMessages(projection session.Projection) []message.Message {
	saved := projection.ModelHistory
	if len(saved.Messages) > 0 && saved.CoveredThroughSequence != nil {
		messages := append([]message.Message(nil), saved.Messages...)
		for _, block := range projection.Blocks {
			if block.Sequence <= *saved.CoveredThroughSequence {
				continue
			}
			if current, ok := blockMessage(block); ok {
				messages = append(messages, current)
			}
		}
		return messages
	}
	messages := make([]message.Message, 0, len(projection.Blocks))
	for _, block := range projection.Blocks {
		if current, ok := blockMessage(block); ok {
			messages = append(messages, current)
		}
	}
	return messages
}

func skillRuntimeToolDefinitions(active, available []hyskill.Skill) []tool.Definition {
	activeNames := make(map[string]struct{}, len(active))
	allNames := make([]string, 0, len(active)+len(available))
	hasResources := false
	for _, current := range active {
		activeNames[current.Name] = struct{}{}
		allNames = append(allNames, current.Name)
		hasResources = hasResources || len(current.Resources) > 0
	}
	availableNames := make([]string, 0, len(available))
	for _, current := range available {
		if _, alreadyActive := activeNames[current.Name]; !alreadyActive {
			availableNames = append(availableNames, current.Name)
			allNames = append(allNames, current.Name)
		}
		hasResources = hasResources || len(current.Resources) > 0
	}
	additional := false
	definitions := make([]tool.Definition, 0, 2)
	if len(availableNames) > 0 {
		definitions = append(definitions, tool.Definition{
			Name:        hyagent.SkillActivationToolName,
			Description: "Load one available Agent Skill before following its instructions.",
			InputSchema: message.JSONSchema{
				Type: "object",
				Properties: map[string]message.JSONSchema{
					"name": {Type: "string", Enum: availableNames},
				},
				Required:             []string{"name"},
				AdditionalProperties: &additional,
			},
			EffectType: tool.EffectReadOnly,
			Idempotent: true,
		})
	}
	if hasResources {
		definitions = append(definitions, tool.Definition{
			Name:        hyagent.SkillResourceToolName,
			Description: "Read one declared resource from an active Agent Skill.",
			InputSchema: message.JSONSchema{
				Type: "object",
				Properties: map[string]message.JSONSchema{
					"skill": {Type: "string", Enum: allNames},
					"path":  {Type: "string"},
				},
				Required:             []string{"skill", "path"},
				AdditionalProperties: &additional,
			},
			EffectType: tool.EffectReadOnly,
			Idempotent: true,
		})
	}
	return definitions
}

func conversationContributions(messages []message.Message) []ContextContribution {
	profile := contextProfileFromRequest(hyprovider.Request{Messages: messages})
	contributions := make([]ContextContribution, 0, len(profile.Contributions))
	for _, contribution := range profile.Contributions {
		if contribution.Category == ContextCategoryConversation {
			contributions = append(contributions, contribution)
		}
	}
	return contributions
}

func skillMessageContributions(text, kind string) []ContextContribution {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if kind != "active" {
		lines := strings.Split(text, "\n")
		contributions := make([]ContextContribution, 0, len(lines))
		overheadBytes := 0
		for _, line := range lines {
			if !strings.HasPrefix(line, "- ") {
				overheadBytes += len(line) + 1
				continue
			}
			name, _, ok := strings.Cut(strings.TrimPrefix(line, "- "), ":")
			if !ok || strings.TrimSpace(name) == "" {
				overheadBytes += len(line) + 1
				continue
			}
			contributions = appendTokenContribution(contributions, ContextCategorySkills, strings.TrimSpace(name), estimateTextTokens(line))
		}
		return appendTokenContribution(contributions, ContextCategorySkills, "catalog.overhead", estimateByteTokens(overheadBytes))
	}

	const startMarker = "--- skill: "
	const endMarker = "--- end skill: "
	contributions := make([]ContextContribution, 0)
	remaining := text
	overheadBytes := 0
	for {
		start := strings.Index(remaining, startMarker)
		if start < 0 {
			overheadBytes += len(remaining)
			break
		}
		overheadBytes += start
		nameStart := start + len(startMarker)
		nameEnd := strings.Index(remaining[nameStart:], " ---")
		if nameEnd < 0 {
			overheadBytes += len(remaining[start:])
			break
		}
		nameEnd += nameStart
		name := strings.TrimSpace(remaining[nameStart:nameEnd])
		endPrefix := endMarker + name + " ---"
		end := strings.Index(remaining[nameEnd:], endPrefix)
		if end < 0 {
			overheadBytes += len(remaining[start:])
			break
		}
		end += nameEnd + len(endPrefix)
		contributions = appendTokenContribution(contributions, ContextCategorySkills, name, estimateTextTokens(remaining[start:end]))
		remaining = remaining[end:]
	}
	return appendTokenContribution(contributions, ContextCategorySkills, "runtime.overhead", estimateByteTokens(overheadBytes))
}

const (
	maxContextContributionsPerCategory = 40
	maxContextContributionNameBytes    = 512
	maxDefinitionEstimateBytes         = 1 << 20
	maxDefinitionEstimateNodes         = 10_000
	maxDefinitionEstimateDepth         = 64
)

func boundedContextContributionName(name string) string {
	if len(name) > maxContextContributionNameBytes {
		name = name[:maxContextContributionNameBytes]
	}
	if !utf8.ValidString(name) {
		name = strings.ToValidUTF8(name, "�")
	}
	if len(name) > maxContextContributionNameBytes {
		name = name[:maxContextContributionNameBytes]
		for !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
	}
	return name
}

func appendToolContribution(contributions []ContextContribution, category ContextCategory, definition tool.Definition) []ContextContribution {
	return appendTokenContribution(contributions, category, definition.Name, estimateToolTokens(definition))
}

func appendTokenContribution(contributions []ContextContribution, category ContextCategory, name string, tokens int) []ContextContribution {
	if tokens <= 0 {
		return contributions
	}
	name = boundedContextContributionName(name)
	categoryCount, remainderIndex := 0, -1
	for index, contribution := range contributions {
		if contribution.Category != category {
			continue
		}
		if contribution.Name == ContextContributionRemainingItems {
			remainderIndex = index
			continue
		}
		categoryCount++
	}
	if categoryCount < maxContextContributionsPerCategory {
		return append(contributions, ContextContribution{Category: category, Name: name, Tokens: tokens})
	}
	if remainderIndex >= 0 {
		contributions[remainderIndex].Tokens = saturatingContextTokenSum(contributions[remainderIndex].Tokens, tokens)
		return contributions
	}
	return append(contributions, ContextContribution{Category: category, Name: ContextContributionRemainingItems, Tokens: tokens})
}

func estimateTextTokens(text string) int {
	return estimateByteTokens(len(text))
}

func estimateByteTokens(bytes int) int {
	if bytes <= 0 {
		return 0
	}
	return (bytes + estimatedBytesPerToken - 1) / estimatedBytesPerToken
}

type definitionEstimate struct {
	bytes int
	nodes int
}

func (e *definitionEstimate) addBytes(value int) bool {
	if value <= 0 || e.bytes >= maxDefinitionEstimateBytes {
		return e.bytes < maxDefinitionEstimateBytes
	}
	if value > maxDefinitionEstimateBytes-e.bytes {
		e.bytes = maxDefinitionEstimateBytes
		return false
	}
	e.bytes += value
	return true
}

func (e *definitionEstimate) addNode() bool {
	if e.nodes >= maxDefinitionEstimateNodes {
		e.bytes = maxDefinitionEstimateBytes
		return false
	}
	e.nodes++
	return true
}

func (e *definitionEstimate) addString(value string) {
	if !e.addNode() || !e.addBytes(2) {
		return
	}
	for index := 0; index < len(value) && e.bytes < maxDefinitionEstimateBytes; index++ {
		switch current := value[index]; {
		case current == '"' || current == '\\':
			e.addBytes(2)
		case current < 0x20:
			e.addBytes(6)
		default:
			e.addBytes(1)
		}
	}
}

func (e *definitionEstimate) addStrings(values []string) {
	for _, value := range values {
		if e.bytes >= maxDefinitionEstimateBytes {
			return
		}
		e.addString(value)
		e.addBytes(1)
	}
}

func (e *definitionEstimate) addSchema(schema message.JSONSchema, depth int) {
	if depth >= maxDefinitionEstimateDepth || !e.addNode() {
		e.bytes = maxDefinitionEstimateBytes
		return
	}
	e.addBytes(32)
	e.addString(schema.Type)
	e.addString(schema.Description)
	e.addStrings(schema.Required)
	e.addStrings(schema.Enum)
	for name, property := range schema.Properties {
		if e.bytes >= maxDefinitionEstimateBytes {
			return
		}
		e.addString(name)
		e.addSchema(property, depth+1)
	}
	if schema.Items != nil {
		e.addSchema(*schema.Items, depth+1)
	}
}

func estimateToolTokens(definition message.ToolDefinition) int {
	estimate := definitionEstimate{bytes: 256}
	for _, value := range []string{
		definition.Name,
		definition.Description,
		definition.Origin,
		definition.RiskLevel,
		string(definition.EffectType),
		definition.Security.RiskLevel,
	} {
		estimate.addString(value)
	}
	estimate.addStrings(definition.Tags)
	estimate.addStrings(definition.RequiredPermissions)
	estimate.addStrings(definition.Security.RequiredPermissions)
	estimate.addStrings(definition.PolicyTags)
	for key, value := range definition.Metadata {
		if estimate.bytes >= maxDefinitionEstimateBytes {
			break
		}
		estimate.addString(key)
		estimate.addString(value)
	}
	estimate.addSchema(definition.InputSchema, 0)
	return estimateByteTokens(estimate.bytes)
}
