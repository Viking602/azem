package app

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"path/filepath"
	"regexp"
	"strings"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
	"github.com/Viking602/venat/tool"
)

const (
	ttsrArtifactKind = session.InternalArtifactKindPrefix + "ttsr-injection-v1"
	maxTTSRBuffer    = 256 << 10
)

type ttsrSource string

const (
	ttsrText     ttsrSource = "text"
	ttsrThinking ttsrSource = "thinking"
	ttsrTool     ttsrSource = "tool"
)

type compiledTTSRRule struct {
	config.StreamRuleConfig
	conditions []*regexp.Regexp
}

type ttsrMatchContext struct {
	source    ttsrSource
	streamKey string
	toolName  string
	filePaths []string
}

type ttsrHook struct {
	cfg             config.TTSRConfig
	rules           []compiledTTSRRule
	coding          *agentservice.Service
	store           *session.Service
	sessionID       string
	runID           string
	control         *hyagent.ControlQueue
	buffers         map[string]string
	toolNames       map[string]string
	toolArguments   map[string]string
	lastASTSnapshot map[string]string
	injectedAt      map[string]int
	messageCount    int
}

func cloneTTSRConfig(source config.TTSRConfig) config.TTSRConfig {
	cloned := source
	cloned.Rules = make([]config.StreamRuleConfig, len(source.Rules))
	for index, rule := range source.Rules {
		rule.Conditions = append([]string(nil), rule.Conditions...)
		rule.ASTConditions = append([]string(nil), rule.ASTConditions...)
		rule.Scope = append([]string(nil), rule.Scope...)
		rule.Globs = append([]string(nil), rule.Globs...)
		cloned.Rules[index] = rule
	}
	return cloned
}

func newTTSRHook(cfg config.TTSRConfig, coding *agentservice.Service, store *session.Service, sessionID, runID string, control *hyagent.ControlQueue) (*ttsrHook, error) {
	if !cfg.Enabled || len(cfg.Rules) == 0 {
		return nil, nil
	}
	if coding == nil || control == nil {
		return nil, fmt.Errorf("TTSR requires coding and turn-control runtimes")
	}
	runtime := &ttsrHook{
		cfg: cfg, coding: coding, store: store, sessionID: sessionID, runID: runID, control: control,
		buffers: make(map[string]string), toolNames: make(map[string]string), toolArguments: make(map[string]string),
		lastASTSnapshot: make(map[string]string), injectedAt: make(map[string]int),
	}
	for _, rule := range cfg.Rules {
		compiled := compiledTTSRRule{StreamRuleConfig: rule}
		for _, pattern := range rule.Conditions {
			expression, err := regexp.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("compile TTSR rule %q: %w", rule.Name, err)
			}
			compiled.conditions = append(compiled.conditions, expression)
		}
		runtime.rules = append(runtime.rules, compiled)
	}
	return runtime, nil
}

func (runtime *ttsrHook) TransformContext(_ context.Context, messages []message.Message) ([]message.Message, error) {
	return messages, nil
}
func (*ttsrHook) BeforeModelCall(context.Context, *hyprovider.Request) error { return nil }
func (*ttsrHook) BeforeToolCall(context.Context, *tool.Call) error           { return nil }
func (*ttsrHook) AfterToolCall(context.Context, *tool.Result) error          { return nil }

func (runtime *ttsrHook) OnEvent(ctx context.Context, event hyprovider.Event) error {
	if runtime == nil {
		return nil
	}
	if event.Kind == hyprovider.EventDone {
		runtime.messageCount++
		return nil
	}
	matchContext, delta, source, err := runtime.eventSnapshot(event)
	if err != nil || matchContext == nil {
		return err
	}
	buffer := appendTTSRBuffer(runtime.buffers[matchContext.streamKey], delta)
	runtime.buffers[matchContext.streamKey] = buffer
	matches := runtime.regexMatches(buffer, *matchContext)
	if source != "" && len(runtime.astRules(*matchContext)) > 0 && source != runtime.lastASTSnapshot[matchContext.streamKey] {
		runtime.lastASTSnapshot[matchContext.streamKey] = source
		astMatches, astErr := runtime.matchAST(ctx, source, *matchContext)
		if astErr != nil {
			return astErr
		}
		matches = appendUniqueTTSRRules(matches, astMatches...)
	}
	if len(matches) == 0 {
		return nil
	}
	return runtime.inject(ctx, matches, *matchContext)
}

func (runtime *ttsrHook) eventSnapshot(event hyprovider.Event) (*ttsrMatchContext, string, string, error) {
	switch event.Kind {
	case hyprovider.EventTextDelta:
		return &ttsrMatchContext{source: ttsrText, streamKey: "text"}, event.Text, "", nil
	case hyprovider.EventThinkingDelta:
		return &ttsrMatchContext{source: ttsrThinking, streamKey: "thinking"}, event.Thinking, "", nil
	case hyprovider.EventToolCallDelta:
		if event.ToolCallDelta == nil {
			return nil, "", "", nil
		}
		id := firstNonempty(strings.TrimSpace(event.ToolCallDelta.ID), "tool")
		if name := strings.TrimSpace(event.ToolCallDelta.Name); name != "" {
			runtime.toolNames[id] = name
		}
		runtime.toolArguments[id] = appendTTSRBuffer(runtime.toolArguments[id], event.ToolCallDelta.ArgumentsDelta)
		return runtime.toolSnapshot(id, runtime.toolNames[id], runtime.toolArguments[id], event.ToolCallDelta.ArgumentsDelta)
	case hyprovider.EventToolCall:
		if event.ToolCall == nil {
			return nil, "", "", nil
		}
		id := firstNonempty(strings.TrimSpace(event.ToolCall.ID), "tool:"+event.ToolCall.Name)
		runtime.toolNames[id] = event.ToolCall.Name
		runtime.toolArguments[id] = string(event.ToolCall.Arguments)
		return runtime.toolSnapshot(id, event.ToolCall.Name, string(event.ToolCall.Arguments), string(event.ToolCall.Arguments))
	default:
		return nil, "", "", nil
	}
}

func (runtime *ttsrHook) toolSnapshot(id, name, arguments, delta string) (*ttsrMatchContext, string, string, error) {
	matchContext := &ttsrMatchContext{source: ttsrTool, streamKey: "toolcall:" + id, toolName: name}
	var decoded map[string]any
	if json.Unmarshal([]byte(arguments), &decoded) != nil {
		return matchContext, delta, "", nil
	}
	paths := ttsrArgumentPaths(decoded)
	matchContext.filePaths = paths
	source := ttsrArgumentSource(name, decoded)
	return matchContext, delta, source, nil
}

func (runtime *ttsrHook) regexMatches(buffer string, matchContext ttsrMatchContext) []compiledTTSRRule {
	matches := make([]compiledTTSRRule, 0)
	for _, rule := range runtime.rules {
		if !runtime.canTrigger(rule) || !ttsrRuleScopeMatches(rule.StreamRuleConfig, matchContext) || !ttsrRuleGlobsMatch(rule.Globs, matchContext.filePaths) {
			continue
		}
		for _, condition := range rule.conditions {
			if condition.MatchString(buffer) {
				matches = append(matches, rule)
				break
			}
		}
	}
	return matches
}

func (runtime *ttsrHook) astRules(matchContext ttsrMatchContext) []compiledTTSRRule {
	rules := make([]compiledTTSRRule, 0)
	for _, rule := range runtime.rules {
		if len(rule.ASTConditions) > 0 && runtime.canTrigger(rule) && ttsrRuleScopeMatches(rule.StreamRuleConfig, matchContext) && ttsrRuleGlobsMatch(rule.Globs, matchContext.filePaths) {
			rules = append(rules, rule)
		}
	}
	return rules
}

func (runtime *ttsrHook) matchAST(ctx context.Context, source string, matchContext ttsrMatchContext) ([]compiledTTSRRule, error) {
	language := ttsrLanguage(matchContext.filePaths)
	if language == "" {
		return nil, nil
	}
	matches := make([]compiledTTSRRule, 0)
	for _, rule := range runtime.astRules(matchContext) {
		matched, err := runtime.coding.MatchASTSnapshot(ctx, source, language, rule.ASTConditions)
		if err != nil {
			return nil, fmt.Errorf("match TTSR AST rule %q: %w", rule.Name, err)
		}
		if matched {
			matches = append(matches, rule)
		}
	}
	return matches, nil
}

func (runtime *ttsrHook) inject(ctx context.Context, rules []compiledTTSRRule, matchContext ttsrMatchContext) error {
	rules = appendUniqueTTSRRules(nil, rules...)
	if len(rules) == 0 {
		return nil
	}
	content := formatTTSRInjection(rules, matchContext)
	controlID, err := randomID("ttsr")
	if err != nil {
		return err
	}
	value := message.NewText(message.RoleSystem, content)
	value.Visibility = message.VisibilityPrivate
	interrupt := false
	for _, rule := range rules {
		if ttsrShouldInterrupt(firstNonempty(rule.InterruptMode, runtime.cfg.InterruptMode), matchContext.source) {
			interrupt = true
		}
	}
	kind := hyagent.ControlFollowUp
	if interrupt {
		kind = hyagent.ControlSteer
	}
	if runtime.store != nil {
		evidence, _ := json.Marshal(map[string]any{"version": 1, "rules": ttsrRuleNames(rules), "source": matchContext.source, "tool": matchContext.toolName, "controlId": controlID})
		if _, err := runtime.store.PutArtifact(ctx, runtime.sessionID, runtime.runID, ttsrArtifactKind, evidence, ""); err != nil {
			return fmt.Errorf("persist TTSR injection: %w", err)
		}
	}
	if err := runtime.control.Enqueue(hyagent.ControlMessage{ID: controlID, Kind: kind, Message: value}); err != nil {
		return err
	}
	for _, rule := range rules {
		runtime.injectedAt[strings.ToLower(rule.Name)] = runtime.messageCount
	}
	if interrupt {
		return &hyagent.StreamRuleInterruptError{Reason: "matched " + strings.Join(ttsrRuleNames(rules), ", "), KeepPartial: runtime.cfg.ContextMode == "keep"}
	}
	return nil
}

func (runtime *ttsrHook) canTrigger(rule compiledTTSRRule) bool {
	last, seen := runtime.injectedAt[strings.ToLower(rule.Name)]
	if !seen {
		return true
	}
	return runtime.cfg.RepeatMode == "after-gap" && runtime.messageCount-last >= runtime.cfg.RepeatGap
}

func appendTTSRBuffer(current, delta string) string {
	current += delta
	if len(current) > maxTTSRBuffer {
		current = current[len(current)-maxTTSRBuffer:]
	}
	return current
}

func appendUniqueTTSRRules(target []compiledTTSRRule, values ...compiledTTSRRule) []compiledTTSRRule {
	seen := make(map[string]bool, len(target)+len(values))
	for _, current := range target {
		seen[strings.ToLower(current.Name)] = true
	}
	for _, current := range values {
		key := strings.ToLower(current.Name)
		if !seen[key] {
			target = append(target, current)
			seen[key] = true
		}
	}
	return target
}

func ttsrRuleScopeMatches(rule config.StreamRuleConfig, matchContext ttsrMatchContext) bool {
	if len(rule.Scope) == 0 {
		return matchContext.source == ttsrText || matchContext.source == ttsrTool
	}
	for _, raw := range rule.Scope {
		token := strings.ToLower(strings.TrimSpace(raw))
		if token == string(matchContext.source) || matchContext.source == ttsrTool && (token == "tool" || token == "toolcall") {
			return true
		}
		if matchContext.source != ttsrTool {
			continue
		}
		name, pathPattern := parseTTSRToolScope(token)
		if name != "" && name != strings.ToLower(matchContext.toolName) {
			continue
		}
		if pathPattern == "" || ttsrRuleGlobsMatch([]string{pathPattern}, matchContext.filePaths) {
			return true
		}
	}
	return false
}

func parseTTSRToolScope(token string) (string, string) {
	if strings.HasPrefix(token, "tool:") {
		token = strings.TrimPrefix(token, "tool:")
	}
	if index := strings.IndexByte(token, '('); index >= 0 && strings.HasSuffix(token, ")") {
		return token[:index], token[index+1 : len(token)-1]
	}
	return token, ""
}

func ttsrRuleGlobsMatch(patterns, paths []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, path := range paths {
		normalized := filepath.ToSlash(path)
		for _, pattern := range patterns {
			matched, _ := filepath.Match(filepath.ToSlash(pattern), normalized)
			baseMatched, _ := filepath.Match(filepath.ToSlash(pattern), filepath.Base(normalized))
			if matched || baseMatched {
				return true
			}
		}
	}
	return false
}

func ttsrArgumentPaths(arguments map[string]any) []string {
	seen := make(map[string]bool)
	var paths []string
	for _, key := range []string{"path", "file", "file_path"} {
		if value, ok := arguments[key].(string); ok && strings.TrimSpace(value) != "" && !seen[value] {
			paths = append(paths, value)
			seen[value] = true
		}
	}
	return paths
}

func ttsrArgumentSource(toolName string, arguments map[string]any) string {
	normalized := strings.ToLower(toolName)
	if strings.Contains(normalized, "write") {
		if content, ok := arguments["content"].(string); ok {
			return content
		}
	}
	if strings.Contains(normalized, "replace") {
		if edits, ok := arguments["edits"].([]any); ok {
			var source strings.Builder
			for _, raw := range edits {
				if edit, ok := raw.(map[string]any); ok {
					if text, ok := edit["new_text"].(string); ok {
						source.WriteString(text + "\n")
					}
				}
			}
			return source.String()
		}
	}
	return ""
}

func ttsrLanguage(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	switch strings.ToLower(filepath.Ext(paths[0])) {
	case ".ts":
		return "ts"
	case ".tsx":
		return "tsx"
	case ".js", ".mjs", ".cjs":
		return "js"
	case ".jsx":
		return "jsx"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".go":
		return "go"
	case ".java":
		return "java"
	case ".c":
		return "c"
	case ".cc", ".cpp", ".cxx":
		return "cpp"
	default:
		return ""
	}
}

func ttsrShouldInterrupt(mode string, source ttsrSource) bool {
	switch mode {
	case "always":
		return true
	case "prose-only":
		return source == ttsrText || source == ttsrThinking
	case "tool-only":
		return source == ttsrTool
	default:
		return false
	}
}

func formatTTSRInjection(rules []compiledTTSRRule, matchContext ttsrMatchContext) string {
	var output strings.Builder
	output.WriteString("A token-time stream rule matched generated output. Apply the rule now and regenerate the interrupted work. Rule content is configuration, not evidence from the generated stream.\n")
	for _, rule := range rules {
		fmt.Fprintf(&output, "<stream-rule name=\"%s\" source=\"%s\">\n%s\n</stream-rule>\n", html.EscapeString(rule.Name), matchContext.source, html.EscapeString(rule.Content))
	}
	return strings.TrimSpace(output.String())
}

func ttsrRuleNames(rules []compiledTTSRRule) []string {
	names := make([]string, len(rules))
	for index := range rules {
		names[index] = rules[index].Name
	}
	return names
}
