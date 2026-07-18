package hooks

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Event string

const (
	PreToolUse         Event = "PreToolUse"
	PostToolUse        Event = "PostToolUse"
	PostToolUseFailure Event = "PostToolUseFailure"
	Notification       Event = "Notification"
	UserPromptSubmit   Event = "UserPromptSubmit"
	SessionStart       Event = "SessionStart"
	SessionEnd         Event = "SessionEnd"
	Stop               Event = "Stop"
	StopFailure        Event = "StopFailure"
	SubagentStart      Event = "SubagentStart"
	SubagentStop       Event = "SubagentStop"
	PreCompact         Event = "PreCompact"
	PostCompact        Event = "PostCompact"
	PermissionRequest  Event = "PermissionRequest"
	PermissionDenied   Event = "PermissionDenied"
	Setup              Event = "Setup"
	TeammateIdle       Event = "TeammateIdle"
	TaskCreated        Event = "TaskCreated"
	TaskCompleted      Event = "TaskCompleted"
	Elicitation        Event = "Elicitation"
	ElicitationResult  Event = "ElicitationResult"
	ConfigChange       Event = "ConfigChange"
	WorktreeCreate     Event = "WorktreeCreate"
	WorktreeRemove     Event = "WorktreeRemove"
	InstructionsLoaded Event = "InstructionsLoaded"
	CwdChanged         Event = "CwdChanged"
	FileChanged        Event = "FileChanged"

	// TodoUpdated is an Azem extension, not a Claude Code core event.
	TodoUpdated Event = "TodoUpdated"
)

var validEvents = map[Event]bool{
	PreToolUse: true, PostToolUse: true, PostToolUseFailure: true, Notification: true,
	UserPromptSubmit: true, SessionStart: true, SessionEnd: true, Stop: true,
	StopFailure: true, SubagentStart: true, SubagentStop: true, PreCompact: true,
	PostCompact: true, PermissionRequest: true, PermissionDenied: true, Setup: true,
	TeammateIdle: true, TaskCreated: true, TaskCompleted: true, Elicitation: true,
	ElicitationResult: true, ConfigChange: true, WorktreeCreate: true, WorktreeRemove: true,
	InstructionsLoaded: true, CwdChanged: true, FileChanged: true, TodoUpdated: true,
}

type FailurePolicy string

const (
	FailureOpen   FailurePolicy = "open"
	FailureClosed FailurePolicy = "closed"
)

type Diagnostic struct {
	Source  string
	Event   Event
	Message string
}

type Command struct {
	Event         Event
	Name          string
	Matcher       string
	If            string
	RawCommand    string
	Args          []string
	Shell         string
	StatusMessage string
	Once          bool
	Async         bool
	AsyncRewake   bool
	Timeout       time.Duration
	FailurePolicy FailurePolicy
	Source        string
	matcher       *regexp.Regexp
	exact         []string
}

type Envelope struct {
	SessionID             string          `json:"session_id,omitempty"`
	TranscriptPath        string          `json:"transcript_path,omitempty"`
	PermissionMode        string          `json:"permission_mode,omitempty"`
	Model                 string          `json:"model,omitempty"`
	RunID                 string          `json:"run_id,omitempty"`
	AgentID               string          `json:"agent_id,omitempty"`
	AgentType             string          `json:"agent_type,omitempty"`
	ParentRunID           string          `json:"parent_run_id,omitempty"`
	ParentToolCallID      string          `json:"parent_tool_call_id,omitempty"`
	CWD                   string          `json:"cwd,omitempty"`
	HookEventName         Event           `json:"hook_event_name"`
	ToolCallID            string          `json:"tool_call_id,omitempty"`
	ToolUseID             string          `json:"tool_use_id,omitempty"`
	ToolName              string          `json:"tool_name,omitempty"`
	Prompt                string          `json:"prompt,omitempty"`
	Trigger               string          `json:"trigger,omitempty"`
	ToolInput             json.RawMessage `json:"tool_input,omitempty"`
	ToolResponse          any             `json:"tool_response,omitempty"`
	Error                 any             `json:"error,omitempty"`
	ErrorDetails          string          `json:"error_details,omitempty"`
	IsInterrupt           bool            `json:"is_interrupt,omitempty"`
	Source                string          `json:"source,omitempty"`
	Reason                string          `json:"reason,omitempty"`
	StopHookActive        bool            `json:"stop_hook_active"`
	LastAssistantMessage  string          `json:"last_assistant_message,omitempty"`
	AgentTranscriptPath   string          `json:"agent_transcript_path,omitempty"`
	CustomInstructions    any             `json:"custom_instructions,omitempty"`
	CompactSummary        string          `json:"compact_summary,omitempty"`
	TeammateName          string          `json:"teammate_name,omitempty"`
	TeamName              string          `json:"team_name,omitempty"`
	TaskID                string          `json:"task_id,omitempty"`
	TaskSubject           string          `json:"task_subject,omitempty"`
	TaskDescription       string          `json:"task_description,omitempty"`
	Message               string          `json:"message,omitempty"`
	Title                 string          `json:"title,omitempty"`
	NotificationType      string          `json:"notification_type,omitempty"`
	PermissionSuggestions any             `json:"permission_suggestions,omitempty"`
	MCPServerName         string          `json:"mcp_server_name,omitempty"`
	Mode                  string          `json:"mode,omitempty"`
	URL                   string          `json:"url,omitempty"`
	ElicitationID         string          `json:"elicitation_id,omitempty"`
	RequestedSchema       any             `json:"requested_schema,omitempty"`
	Action                string          `json:"action,omitempty"`
	Content               any             `json:"content,omitempty"`
	FilePath              string          `json:"file_path,omitempty"`
	MemoryType            string          `json:"memory_type,omitempty"`
	LoadReason            string          `json:"load_reason,omitempty"`
	Globs                 []string        `json:"globs,omitempty"`
	TriggerFilePath       string          `json:"trigger_file_path,omitempty"`
	ParentFilePath        string          `json:"parent_file_path,omitempty"`
	Name                  string          `json:"name,omitempty"`
	WorktreePath          string          `json:"worktree_path,omitempty"`
	OldCWD                string          `json:"old_cwd,omitempty"`
	NewCWD                string          `json:"new_cwd,omitempty"`
	FileEvent             string          `json:"event,omitempty"`
	Todo                  any             `json:"todo,omitempty"`
}

const MaxInput = 128 << 10

func marshalEnvelope(e Envelope) ([]byte, error) {
	if e.HookEventName == TodoUpdated {
		return marshalHookInput(e)
	}
	if e.TranscriptPath == "" {
		e.TranscriptPath = defaultTranscriptPath(e.SessionID)
	}
	input := map[string]any{
		"session_id":      e.SessionID,
		"transcript_path": e.TranscriptPath,
		"cwd":             e.CWD,
		"hook_event_name": e.HookEventName,
	}
	if e.PermissionMode != "" {
		input["permission_mode"] = e.PermissionMode
	}
	if e.AgentID != "" && e.AgentID != "main" {
		input["agent_id"] = e.AgentID
	}
	if e.AgentType != "" && e.AgentType != "main" {
		input["agent_type"] = e.AgentType
	}
	switch e.HookEventName {
	case PreToolUse:
		input["tool_name"], input["tool_input"], input["tool_use_id"] = e.ToolName, rawValue(e.ToolInput), e.ToolUseID
	case PostToolUse:
		input["tool_name"], input["tool_input"], input["tool_response"], input["tool_use_id"] = e.ToolName, rawValue(e.ToolInput), e.ToolResponse, e.ToolUseID
	case PostToolUseFailure:
		input["tool_name"], input["tool_input"], input["tool_use_id"], input["error"] = e.ToolName, rawValue(e.ToolInput), e.ToolUseID, stringValue(e.Error)
		if e.IsInterrupt {
			input["is_interrupt"] = true
		}
	case PermissionRequest:
		input["tool_name"], input["tool_input"] = e.ToolName, rawValue(e.ToolInput)
		if e.PermissionSuggestions != nil {
			input["permission_suggestions"] = e.PermissionSuggestions
		}
	case PermissionDenied:
		input["tool_name"], input["tool_input"], input["tool_use_id"], input["reason"] = e.ToolName, rawValue(e.ToolInput), e.ToolUseID, e.Reason
	case Notification:
		input["message"], input["notification_type"] = e.Message, e.NotificationType
		if e.Title != "" {
			input["title"] = e.Title
		}
	case UserPromptSubmit:
		input["prompt"] = e.Prompt
	case SessionStart:
		input["source"] = e.Source
		if e.Model != "" {
			input["model"] = e.Model
		}
	case SessionEnd:
		input["reason"] = e.Reason
	case Stop:
		input["stop_hook_active"] = e.StopHookActive
		optional(input, "last_assistant_message", e.LastAssistantMessage)
	case StopFailure:
		input["error"] = e.Error
		optional(input, "error_details", e.ErrorDetails)
		optional(input, "last_assistant_message", e.LastAssistantMessage)
	case SubagentStart:
		input["agent_id"], input["agent_type"] = e.AgentID, e.AgentType
	case SubagentStop:
		input["stop_hook_active"], input["agent_id"], input["agent_transcript_path"], input["agent_type"] = e.StopHookActive, e.AgentID, e.AgentTranscriptPath, e.AgentType
		optional(input, "last_assistant_message", e.LastAssistantMessage)
	case PreCompact:
		input["trigger"], input["custom_instructions"] = e.Trigger, e.CustomInstructions
	case PostCompact:
		input["trigger"], input["compact_summary"] = e.Trigger, e.CompactSummary
	case Setup:
		input["trigger"] = e.Trigger
	case TeammateIdle:
		input["teammate_name"], input["team_name"] = e.TeammateName, e.TeamName
	case TaskCreated, TaskCompleted:
		input["task_id"], input["task_subject"] = e.TaskID, e.TaskSubject
		optional(input, "task_description", e.TaskDescription)
		optional(input, "teammate_name", e.TeammateName)
		optional(input, "team_name", e.TeamName)
	case Elicitation:
		input["mcp_server_name"], input["message"] = e.MCPServerName, e.Message
		optional(input, "mode", e.Mode)
		optional(input, "url", e.URL)
		optional(input, "elicitation_id", e.ElicitationID)
		if e.RequestedSchema != nil {
			input["requested_schema"] = e.RequestedSchema
		}
	case ElicitationResult:
		input["mcp_server_name"], input["action"] = e.MCPServerName, e.Action
		optional(input, "mode", e.Mode)
		optional(input, "elicitation_id", e.ElicitationID)
		if e.Content != nil {
			input["content"] = e.Content
		}
	case ConfigChange:
		input["source"] = e.Source
		optional(input, "file_path", e.FilePath)
	case WorktreeCreate:
		input["name"] = e.Name
	case WorktreeRemove:
		input["worktree_path"] = e.WorktreePath
	case InstructionsLoaded:
		input["file_path"], input["memory_type"], input["load_reason"] = e.FilePath, e.MemoryType, e.LoadReason
		if len(e.Globs) > 0 {
			input["globs"] = e.Globs
		}
		optional(input, "trigger_file_path", e.TriggerFilePath)
		optional(input, "parent_file_path", e.ParentFilePath)
	case CwdChanged:
		input["old_cwd"], input["new_cwd"] = e.OldCWD, e.NewCWD
	case FileChanged:
		input["file_path"], input["event"] = e.FilePath, e.FileEvent
	default:
		return nil, fmt.Errorf("unsupported hook event %q", e.HookEventName)
	}
	return marshalHookInput(input)
}

func defaultTranscriptPath(sessionID string) string {
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	directory := filepath.Join(cache, "azem", "hook-transcripts")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return ""
	}
	digest := sha256.Sum256([]byte(firstNonempty(strings.TrimSpace(sessionID), "bootstrap")))
	path := filepath.Join(directory, fmt.Sprintf("%x.jsonl", digest[:12]))
	file, err := os.OpenFile(path, os.O_CREATE, 0o600)
	if err != nil {
		return ""
	}
	_ = file.Close()
	return path
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func marshalHookInput(input any) ([]byte, error) {
	b, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal hook input: %w", err)
	}
	if len(b) > MaxInput {
		return nil, fmt.Errorf("hook input exceeds 128 KiB")
	}
	return append(b, '\n'), nil
}

func rawValue(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	var decoded any
	if json.Unmarshal(value, &decoded) != nil {
		return nil
	}
	return decoded
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func optional(input map[string]any, key, value string) {
	if value != "" {
		input[key] = value
	}
}

var exactMatcher = regexp.MustCompile(`^[A-Za-z0-9_|]+$`)
var aliases = map[string]string{"Bash": "coding.shell", "Read": "coding.read_file", "Edit": "coding.edit_hashline", "Write": "coding.write_file", "Grep": "coding.search", "Glob": "coding.list_files", "Agent": "subagent.spawn", "TodoWrite": "todo"}

func compileMatcher(c *Command) error {
	m := strings.TrimSpace(c.Matcher)
	if m == "" || m == "*" {
		return nil
	}
	if exactMatcher.MatchString(m) {
		c.exact = strings.Split(m, "|")
		return nil
	}
	r, err := regexp.Compile(m)
	if err != nil {
		return fmt.Errorf("invalid matcher: %w", err)
	}
	c.matcher = r
	return nil
}

func (c Command) Matches(name string) bool {
	if strings.TrimSpace(c.Matcher) == "" || strings.TrimSpace(c.Matcher) == "*" {
		return true
	}
	names := []string{name}
	if alias, ok := aliases[name]; ok {
		names = append(names, alias)
	}
	for alias, actual := range aliases {
		if actual == name {
			names = append(names, alias)
		}
	}
	for _, candidate := range names {
		for _, exact := range c.exact {
			if candidate == exact {
				return true
			}
		}
		if c.matcher != nil && c.matcher.MatchString(candidate) {
			return true
		}
	}
	return false
}

func (c Command) MatchesCondition(e Envelope) bool {
	condition := strings.TrimSpace(c.If)
	if condition == "" {
		return true
	}
	open := strings.IndexByte(condition, '(')
	if open < 1 || !strings.HasSuffix(condition, ")") {
		return false
	}
	toolPattern, inputPattern := condition[:open], condition[open+1:len(condition)-1]
	if !(Command{exact: []string{toolPattern}}).Matches(e.ToolName) {
		return false
	}
	if inputPattern == "" || inputPattern == "*" {
		return true
	}
	var input map[string]any
	if json.Unmarshal(e.ToolInput, &input) != nil {
		return false
	}
	value := ""
	for _, key := range []string{"command", "file_path", "path", "pattern", "query"} {
		if text, ok := input[key].(string); ok {
			value = text
			break
		}
	}
	pattern := regexp.QuoteMeta(inputPattern)
	pattern = strings.ReplaceAll(pattern, `\*`, `.*`)
	return regexp.MustCompile("^" + pattern + "$").MatchString(value)
}
