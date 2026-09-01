package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	agentservice "github.com/Viking602/azem/internal/agent"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/tool"
)

const (
	vibeSpawnTool            = "vibe_spawn"
	vibeSendTool             = "vibe_send"
	vibeWaitTool             = "vibe_wait"
	vibeKillTool             = "vibe_kill"
	vibeRegistryArtifactKind = session.InternalArtifactKindPrefix + "vibe-registry-v1"
	vibeListTool             = "vibe_list"
)

type vibeRecord struct {
	Name       string `json:"id"`
	CLI        string `json:"cli"`
	RunID      string `json:"runId"`
	State      string `json:"state"`
	Turns      int    `json:"turns"`
	Queued     int    `json:"queued"`
	Model      string `json:"model,omitempty"`
	LastOutput string `json:"lastOutput,omitempty"`
	LastError  string `json:"lastError,omitempty"`
}
type vibeRegistryState struct {
	Version int          `json:"version"`
	SavedAt time.Time    `json:"savedAt"`
	Records []vibeRecord `json:"records"`
}

type vibeDriver struct {
	operation string
	runtime   *subagentRuntime
	parent    subagentParentRuntime
	routes    config.VibeConfig
}

func vibeDirectorWorkspaceTool(name string) bool {
	return name == "coding.read_file"
}

func newVibeDrivers(runtime *subagentRuntime, parent subagentParentRuntime, routes config.VibeConfig) ([]tool.Driver, error) {
	if runtime == nil {
		return nil, nil
	}
	parent.DirectorReadOnly = true
	if err := restoreVibeRegistry(runtime, parent); err != nil {
		return nil, err
	}
	return []tool.Driver{
		&vibeDriver{operation: vibeSpawnTool, runtime: runtime, parent: parent, routes: routes},
		&vibeDriver{operation: vibeSendTool, runtime: runtime, parent: parent, routes: routes},
		&vibeDriver{operation: vibeWaitTool, runtime: runtime, parent: parent, routes: routes},
		&vibeDriver{operation: vibeKillTool, runtime: runtime, parent: parent, routes: routes},
		&vibeDriver{operation: vibeListTool, runtime: runtime, parent: parent, routes: routes},
	}, nil
}

func (driver *vibeDriver) Definition() tool.Definition {
	additional := false
	definition := tool.Definition{Concurrency: tool.ConcurrencyParallel}
	switch driver.operation {
	case vibeSpawnTool:
		definition.Name, definition.Description = vibeSpawnTool, "Start one persistent Vibe worker session. fast uses the low-latency route for mechanical work; good uses the strong route for design and difficult debugging. The worker starts blank, so prompt must include files, constraints, acceptance, and evidence. Returns immediately."
		definition.InputSchema = tool.Schema{Type: "object", Required: []string{"cli", "prompt"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{
			"cli": {Type: "string", Enum: []string{"fast", "good"}}, "name": {Type: "string"}, "prompt": {Type: "string"},
		}}
	case vibeSendTool:
		definition.Name, definition.Description = vibeSendTool, "Message an existing Vibe worker by stable session name. A running worker is steered; an idle/parked worker starts its next turn with prior conversation context. Never respawn the same workstream."
		definition.InputSchema = tool.Schema{Type: "object", Required: []string{"session", "message"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{"session": {Type: "string"}, "message": {Type: "string"}}}
	case vibeWaitTool:
		definition.Name, definition.Description = vibeWaitTool, "Wait until the first selected Vibe worker turn settles. Omit sessions to watch all running Vibe workers. timeout is seconds, default 30."
		definition.InputSchema = tool.Schema{Type: "object", AdditionalProperties: &additional, Properties: map[string]tool.Schema{"sessions": {Type: "array", Items: &tool.Schema{Type: "string"}}, "timeout": {Type: "integer"}}}
	case vibeKillTool:
		definition.Name, definition.Description = vibeKillTool, "Terminate one Vibe worker session and cancel its current turn."
		definition.InputSchema = tool.Schema{Type: "object", Required: []string{"session"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{"session": {Type: "string"}}}
	default:
		definition.Name, definition.Description = vibeListTool, "List persistent Vibe worker sessions and their fast/good, running/idle/dead, turn, model, and latest-result state."
		definition.InputSchema = tool.Schema{Type: "object", AdditionalProperties: &additional}
	}
	return definition
}

func (driver *vibeDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	switch driver.operation {
	case vibeSpawnTool:
		return driver.spawn(ctx, call), nil
	case vibeSendTool:
		return driver.send(ctx, call), nil
	case vibeWaitTool:
		return driver.wait(ctx, call), nil
	case vibeKillTool:
		return driver.kill(ctx, call), nil
	default:
		return driver.list(call), nil
	}
}

func (driver *vibeDriver) spawn(ctx context.Context, call tool.Call) tool.Result {
	var input struct {
		CLI    string `json:"cli"`
		Name   string `json:"name"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return vibeError(call, err)
	}
	input.CLI, input.Name, input.Prompt = strings.TrimSpace(input.CLI), strings.TrimSpace(input.Name), strings.TrimSpace(input.Prompt)
	if input.CLI != "fast" && input.CLI != "good" {
		return vibeError(call, fmt.Errorf("cli must be fast or good"))
	}
	if input.Prompt == "" || len([]rune(input.Prompt)) > 100_000 {
		return vibeError(call, fmt.Errorf("prompt is required and must not exceed 100000 characters"))
	}
	if input.Name == "" {
		id, err := randomID(input.CLI)
		if err != nil {
			return vibeError(call, err)
		}
		input.Name = id
	}
	if !validSubagentBatchName(input.Name) {
		return vibeError(call, fmt.Errorf("name must contain 1-48 letters, numbers, underscores, or hyphens"))
	}
	driver.runtime.mu.Lock()
	if record, exists := driver.runtime.vibe[strings.ToLower(input.Name)]; exists && record.State != "dead" {
		driver.runtime.mu.Unlock()
		return vibeError(call, fmt.Errorf("vibe session %q already exists; use vibe_send", input.Name))
	}
	driver.runtime.mu.Unlock()
	route := driver.routes.Fast
	if input.CLI == "good" {
		route = driver.routes.Good
	}
	spawnInput := subagentSpawnInput{
		Name: input.Name, Prompt: input.Prompt, Description: input.Name, SubagentType: "worker", SubagentTypeSet: true,
		CapabilityMode: "all", CapabilityModeSet: true, Isolation: "none", IsolationSet: true, Background: true, BackgroundSet: true,
		Provider: route.Provider, Model: route.Model, Reasoning: route.Reasoning, parentToolCallID: call.ID,
	}
	run, err := driver.runtime.spawn(spawnInput, driver.parent, nil)
	if err != nil {
		return vibeError(call, err)
	}
	record := vibeRecord{Name: input.Name, CLI: input.CLI, RunID: run.ID, State: "running", Model: firstNonempty(route.Model, driver.parent.ModelID)}
	driver.runtime.mu.Lock()
	if driver.runtime.vibe == nil {
		driver.runtime.vibe = make(map[string]vibeRecord)
	}
	driver.runtime.vibe[strings.ToLower(input.Name)] = record
	driver.runtime.mu.Unlock()
	if err := driver.persistRegistry(ctx); err != nil {
		driver.runtime.Cancel(driver.parent.SessionID, run.ID)
		return vibeError(call, err)
	}
	return vibeResult(call, fmt.Sprintf("Spawned %s session `%s`. Its turn runs in the background; keep directing other sessions or call vibe_wait when blocked.", input.CLI, input.Name), map[string]any{"op": "spawn", "spawned": record, "screens": driver.screens(nil)})
}

func (driver *vibeDriver) send(ctx context.Context, call tool.Call) tool.Result {
	var input struct {
		Session string `json:"session"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return vibeError(call, err)
	}
	input.Session, input.Message = strings.TrimSpace(input.Session), strings.TrimSpace(input.Message)
	if input.Session == "" || input.Message == "" || len([]rune(input.Message)) > 100_000 {
		return vibeError(call, fmt.Errorf("session and message are required; message must not exceed 100000 characters"))
	}
	record, exists := driver.record(input.Session)
	if !exists || record.State == "dead" {
		return vibeError(call, fmt.Errorf("vibe session %q is unavailable", input.Session))
	}
	mode := "turn"
	if driver.activeRunID(input.Session) != "" {
		mode = "steered"
	}
	response, err := driver.runtime.ExecuteHubPeer(ctx, agentservice.HubPeerRequest{Operation: "send", Caller: agentservice.Invocation{AgentID: "azem-main", TeamRunID: driver.parent.ParentRunID}, Params: map[string]any{"to": input.Session, "message": input.Message}})
	if err != nil || response.IsError {
		if err == nil {
			err = fmt.Errorf("%s", response.Content)
		}
		return vibeError(call, err)
	}
	if runID := driver.activeRunID(input.Session); runID != "" {
		record.RunID, record.State = runID, "running"
	}
	driver.saveRecord(record)
	if err := driver.persistRegistry(ctx); err != nil {
		return vibeError(call, err)
	}
	modeLabel := strings.ToUpper(mode[:1]) + mode[1:]
	return vibeResult(call, fmt.Sprintf("%s `%s`; the worker keeps its prior conversation.", modeLabel, input.Session), map[string]any{"op": "send", "send": map[string]any{"id": input.Session, "mode": mode}, "screens": driver.screens(nil)})
}

func (driver *vibeDriver) wait(ctx context.Context, call tool.Call) tool.Result {
	var input struct {
		Sessions []string `json:"sessions"`
		Timeout  int      `json:"timeout"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return vibeError(call, err)
	}
	if input.Timeout < 0 || input.Timeout > 3600 {
		return vibeError(call, fmt.Errorf("timeout must be between 0 and 3600 seconds"))
	}
	selected := input.Sessions
	if len(selected) == 0 {
		for _, record := range driver.screens(nil) {
			if record.State == "running" || record.State == "starting" {
				selected = append(selected, record.Name)
			}
		}
	}
	ids := make([]string, 0, len(selected))
	for _, name := range selected {
		if record, exists := driver.record(name); exists && record.State != "dead" && record.RunID != "" {
			ids = append(ids, record.RunID)
		}
	}
	if len(ids) == 0 {
		return vibeResult(call, "No Vibe turns in flight to wait for.", map[string]any{"op": "wait", "screens": driver.screens(selected), "settled": []any{}, "stillRunning": []string{}, "timedOut": false})
	}
	timeout := 30 * time.Second
	if input.Timeout > 0 {
		timeout = time.Duration(input.Timeout) * time.Second
	}
	snapshots := driver.runtime.Query(ctx, driver.parent.SessionID, ids, timeout)
	settled := make([]map[string]any, 0)
	stillRunning := make([]string, 0)
	for _, snapshot := range snapshots {
		name, record, exists := driver.recordByRun(snapshot.Run.ID)
		if !exists {
			continue
		}
		if snapshot.Found && subagentTerminal(snapshot.Run.State) {
			record.State, record.Turns, record.LastOutput, record.LastError = "idle", record.Turns+1, snapshot.Run.Output, snapshot.Run.Error
			driver.saveRecord(record)
			settled = append(settled, map[string]any{"id": name, "status": snapshot.Run.State, "resultText": snapshot.Run.Output, "error": snapshot.Run.Error})
		} else {
			stillRunning = append(stillRunning, name)
		}
	}
	if err := driver.persistRegistry(ctx); err != nil {
		return vibeError(call, err)
	}
	timedOut := len(settled) == 0 && len(stillRunning) > 0
	content := ""
	for _, item := range settled {
		content += fmt.Sprintf("## `%s` — %s\n%s\n", item["id"], item["status"], item["resultText"])
	}
	if len(stillRunning) > 0 {
		content += "Still running: " + strings.Join(stillRunning, ", ") + "."
	}
	return vibeResult(call, strings.TrimSpace(content), map[string]any{"op": "wait", "screens": driver.screens(selected), "settled": settled, "stillRunning": stillRunning, "timedOut": timedOut})
}

func (driver *vibeDriver) kill(ctx context.Context, call tool.Call) tool.Result {
	var input struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return vibeError(call, err)
	}
	input.Session = strings.TrimSpace(input.Session)
	record, exists := driver.record(input.Session)
	if !exists {
		return vibeError(call, fmt.Errorf("vibe session %q was not found", input.Session))
	}
	cancelled := false
	if runID := driver.activeRunID(input.Session); runID != "" {
		outcome := driver.runtime.Cancel(driver.parent.SessionID, runID)
		cancelled = outcome.Outcome != "not_found"
	}
	driver.runtime.mu.Lock()
	for id, parked := range driver.runtime.parked {
		if strings.EqualFold(parked.name, input.Session) {
			delete(driver.runtime.parked, id)
		}
	}
	driver.runtime.mu.Unlock()
	record.State = "dead"
	driver.saveRecord(record)
	if err := driver.persistRegistry(ctx); err != nil {
		return vibeError(call, err)
	}
	return vibeResult(call, fmt.Sprintf("Killed Vibe session `%s`.", input.Session), map[string]any{"op": "kill", "killed": map[string]any{"id": input.Session, "cancelledTurn": cancelled}, "screens": driver.screens(nil)})
}

func (driver *vibeDriver) list(call tool.Call) tool.Result {
	screens := driver.screens(nil)
	if len(screens) == 0 {
		return vibeResult(call, "No Vibe sessions. Spawn one with vibe_spawn.", map[string]any{"op": "list", "screens": screens})
	}
	lines := make([]string, len(screens))
	for index, screen := range screens {
		lines[index] = fmt.Sprintf("- `%s` [%s] %s · %d turns", screen.Name, screen.CLI, screen.State, screen.Turns)
		if screen.Model != "" {
			lines[index] += " · " + screen.Model
		}
	}
	return vibeResult(call, strings.Join(lines, "\n"), map[string]any{"op": "list", "screens": screens})
}

func (driver *vibeDriver) screens(filter []string) []vibeRecord {
	driver.runtime.mu.Lock()
	defer driver.runtime.mu.Unlock()
	allowed := make(map[string]bool, len(filter))
	for _, name := range filter {
		allowed[strings.ToLower(name)] = true
	}
	result := make([]vibeRecord, 0, len(driver.runtime.vibe))
	for key, record := range driver.runtime.vibe {
		if len(allowed) > 0 && !allowed[key] {
			continue
		}
		if runID := driver.activeRunIDLocked(record.Name); runID != "" {
			record.RunID, record.State = runID, "running"
		} else if record.State != "dead" {
			record.State = "idle"
		}
		result = append(result, record)
	}
	return result
}

func (driver *vibeDriver) record(name string) (vibeRecord, bool) {
	driver.runtime.mu.Lock()
	defer driver.runtime.mu.Unlock()
	record, exists := driver.runtime.vibe[strings.ToLower(name)]
	return record, exists
}

func (driver *vibeDriver) recordByRun(runID string) (string, vibeRecord, bool) {
	driver.runtime.mu.Lock()
	defer driver.runtime.mu.Unlock()
	for _, record := range driver.runtime.vibe {
		if record.RunID == runID {
			return record.Name, record, true
		}
	}
	return "", vibeRecord{}, false
}

func (driver *vibeDriver) saveRecord(record vibeRecord) {
	driver.runtime.mu.Lock()
	if driver.runtime.vibe == nil {
		driver.runtime.vibe = make(map[string]vibeRecord)
	}
	driver.runtime.vibe[strings.ToLower(record.Name)] = record
	driver.runtime.mu.Unlock()
}

func (driver *vibeDriver) activeRunID(name string) string {
	driver.runtime.mu.Lock()
	defer driver.runtime.mu.Unlock()
	return driver.activeRunIDLocked(name)
}

func restoreVibeRegistry(runtime *subagentRuntime, parent subagentParentRuntime) error {
	if parent.Host == nil || parent.Host.Sessions() == nil {
		return nil
	}
	artifact, err := parent.Host.Sessions().LoadLatestArtifactByKind(runtime.ctx, parent.SessionID, vibeRegistryArtifactKind)
	if errors.Is(err, session.ErrContextArtifactNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load Vibe registry: %w", err)
	}
	var state vibeRegistryState
	if err := json.Unmarshal(artifact.Payload, &state); err != nil {
		return fmt.Errorf("decode Vibe registry: %w", err)
	}
	if state.Version != 1 {
		return fmt.Errorf("Vibe registry version %d is unsupported", state.Version)
	}
	runtime.mu.Lock()
	if runtime.vibe == nil {
		runtime.vibe = make(map[string]vibeRecord)
	}
	for _, record := range state.Records {
		if record.Name != "" {
			runtime.vibe[strings.ToLower(record.Name)] = record
		}
	}
	runtime.mu.Unlock()
	return nil
}

func (driver *vibeDriver) persistRegistry(ctx context.Context) error {
	if driver.parent.Host == nil || driver.parent.Host.Sessions() == nil {
		return nil
	}
	driver.runtime.mu.Lock()
	records := make([]vibeRecord, 0, len(driver.runtime.vibe))
	for _, record := range driver.runtime.vibe {
		records = append(records, record)
	}
	driver.runtime.mu.Unlock()
	sort.Slice(records, func(i, j int) bool { return strings.ToLower(records[i].Name) < strings.ToLower(records[j].Name) })
	payload, err := json.Marshal(vibeRegistryState{Version: 1, SavedAt: time.Now().UTC(), Records: records})
	if err != nil {
		return err
	}
	if _, err := driver.parent.Host.Sessions().PutArtifact(ctx, driver.parent.SessionID, driver.parent.ParentRunID, vibeRegistryArtifactKind, payload, ""); err != nil {
		return fmt.Errorf("persist Vibe registry: %w", err)
	}
	return nil
}

func (driver *vibeDriver) activeRunIDLocked(name string) string {
	for id, active := range driver.runtime.active {
		if strings.EqualFold(active.name, name) {
			return id
		}
	}
	return ""
}

func vibeResult(call tool.Call, content string, details map[string]any) tool.Result {
	structured, _ := json.Marshal(details)
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: content, Structured: structured}
}

func vibeError(call tool.Call, err error) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: call.Name + " failed: " + err.Error(), IsError: true}
}
