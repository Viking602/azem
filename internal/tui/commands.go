package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Viking602/azem/internal/app"
)

type Runtime interface {
	NextEvent(context.Context) (app.Event, error)
	StartTurn(string) (string, error)
	CancelActive() bool
}

type (
	ActionKind   = app.ActionKind
	ApprovalMode = app.ApprovalMode
)

const (
	ApprovalModePrompt     = app.ApprovalModePrompt
	ApprovalModeAutoReview = app.ApprovalModeAutoReview
	ApprovalModeYolo       = app.ApprovalModeYolo
)

const (
	ActionLogin            = app.ActionLogin
	ActionLogout           = app.ActionLogout
	ActionNewSession       = app.ActionNewSession
	ActionListSessions     = app.ActionListSessions
	ActionResumeSession    = app.ActionResumeSession
	ActionCompact          = app.ActionCompact
	ActionResolveApproval  = app.ActionResolveApproval
	ActionSetApprovalMode  = app.ActionSetApprovalMode
	ActionSetLanguage      = app.ActionSetLanguage
	ActionReconcileAttempt = app.ActionReconcileAttempt
	ActionInspectAgent     = app.ActionInspectAgent
	ActionListAgentTypes   = app.ActionListAgentTypes
	ActionListPersonas     = app.ActionListPersonas
	ActionCancelAgent      = app.ActionCancelAgent
	ActionRefreshMCP       = app.ActionRefreshMCP
	ActionReconnectMCP     = app.ActionReconnectMCP
	ActionListSkills       = app.ActionListSkills
	ActionReloadSkills     = app.ActionReloadSkills
	ActionListMemories     = app.ActionListMemories
	ActionRemember         = app.ActionRemember
	ActionForgetMemory     = app.ActionForgetMemory
	ActionShowRecap        = app.ActionShowRecap
	ActionListModelRoutes  = app.ActionListModelRoutes
	ActionSetModelRoute    = app.ActionSetModelRoute
	ActionResetModelRoute  = app.ActionResetModelRoute
	ActionListBackground   = app.ActionListBackground
	ActionStartBackground  = app.ActionStartBackground
	ActionStopBackground   = app.ActionStopBackground
	ActionLogsBackground   = app.ActionLogsBackground
)

type (
	Action        = app.Action
	ActionRuntime = app.ActionExecutor
)

var errActionUnsupported = errors.New("this action is unavailable in the current runtime")

type Command struct {
	Name string
	Args []string
}

type SlashCommand struct {
	Name   string
	Usage  string
	Detail string
}

var slashCommands = []SlashCommand{
	{Name: "models", Usage: "/models"},
	{Name: "model-routing", Usage: "/model-routing"},
	{Name: "skills", Usage: "/skills [reload]"}, {Name: "skill", Usage: "/skill <name> [instruction]"},
	{Name: "provider", Usage: "/provider [chatgpt|grok]"}, {Name: "reasoning", Usage: "/reasoning [level]"},
	{Name: "login", Usage: "/login [chatgpt|grok]"}, {Name: "logout", Usage: "/logout [chatgpt|grok]"},
	{Name: "team", Usage: "/team on|off"}, {Name: "agents", Usage: "/agents [cancel <id>]"},
	{Name: "background", Usage: "/background [start [--name NAME] [--cwd DIR] -- COMMAND | stop <id> | logs <id>]"},
	{Name: "todos", Usage: "/todos"}, {Name: "todo", Usage: "/todo"},
	{Name: "agent-types", Usage: "/agent-types"}, {Name: "personas", Usage: "/personas"},
	{Name: "new", Usage: "/new"}, {Name: "sessions", Usage: "/sessions"}, {Name: "resume", Usage: "/resume"},
	{Name: "compact", Usage: "/compact"}, {Name: "mcp", Usage: "/mcp [refresh|reconnect <server>]"},
	{Name: "memory", Usage: "/memory [query]"}, {Name: "remember", Usage: "/remember <text>"},
	{Name: "forget", Usage: "/forget <memory-id>"}, {Name: "recap", Usage: "/recap"},
	{Name: "reconcile", Usage: "/reconcile <attempt-id> <result>"}, {Name: "cancel", Usage: "/cancel"},
	{Name: "status", Usage: "/status"},
	{Name: "language", Usage: "/language <en|zh-CN>"}, {Name: "help", Usage: "/help"}, {Name: "quit", Usage: "/quit"},
}

type scoredSlashCommand struct {
	command SlashCommand
	score   int
	order   int
}

func commandSuggestions(input string) []SlashCommand {
	query, ok := slashCommandQuery(input)
	if !ok {
		return nil
	}
	matches := make([]scoredSlashCommand, 0, len(slashCommands))
	for order, command := range slashCommands {
		score, matched := fuzzyCommandScore(command.Name, query)
		if matched {
			matches = append(matches, scoredSlashCommand{command: command, score: score, order: order})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].order < matches[j].order
	})
	result := make([]SlashCommand, len(matches))
	for index := range matches {
		result[index] = matches[index].command
	}
	return result
}

func slashCommandQuery(input string) (string, bool) {
	if !strings.HasPrefix(input, "/") || strings.ContainsAny(input, " \t\r\n") {
		return "", false
	}
	return strings.ToLower(strings.TrimPrefix(input, "/")), true
}

func fuzzyCommandScore(candidate string, query string) (int, bool) {
	if query == "" {
		return 0, true
	}
	if candidate == query {
		return 4000, true
	}
	if strings.HasPrefix(candidate, query) {
		return 3000 - len(candidate), true
	}
	if index := strings.Index(candidate, query); index >= 0 {
		return 2000 - index*10 - len(candidate), true
	}
	position := 0
	gaps := 0
	for index := 0; index < len(query); index++ {
		offset := strings.IndexByte(candidate[position:], query[index])
		if offset < 0 {
			return 0, false
		}
		gaps += offset
		position += offset + 1
	}
	return 1000 - gaps*10 - len(candidate), true
}

func exactSlashCommand(input string) bool {
	query, ok := slashCommandQuery(input)
	if !ok {
		return false
	}
	for _, command := range slashCommands {
		if command.Name == query {
			return true
		}
	}
	return false
}

func ParseCommand(input string) (Command, bool, error) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return Command{}, false, nil
	}
	fields := strings.Fields(strings.TrimPrefix(trimmed, "/"))
	if len(fields) == 0 {
		return Command{}, true, fmt.Errorf("empty command")
	}
	name := strings.ToLower(fields[0])
	if name == "langauge" {
		name = "language"
	}
	known := false
	for _, command := range slashCommands {
		if command.Name == name {
			known = true
			break
		}
	}
	if !known {
		return Command{}, false, nil
	}
	return Command{Name: name, Args: fields[1:]}, true, nil
}

func waitForAppEvent(runtime Runtime) tea.Cmd {
	return func() tea.Msg {
		event, err := runtime.NextEvent(context.Background())
		if err != nil {
			return appStreamClosedMsg{Err: err}
		}
		return appEventMsg{Event: event}
	}
}

func startTurn(runtime Runtime, request app.TurnRequest) tea.Cmd {
	return func() tea.Msg {
		var runID string
		var err error
		if configured, ok := runtime.(interface {
			StartConfiguredTurn(app.TurnRequest) (string, error)
		}); ok {
			runID, err = configured.StartConfiguredTurn(request)
		} else {
			runID, err = runtime.StartTurn(request.Prompt)
		}
		return startTurnResultMsg{RunID: runID, Err: err}
	}
}

func cancelTurn(runtime Runtime, children bool) tea.Cmd {
	return func() tea.Msg {
		if scoped, ok := runtime.(interface{ CancelActiveWithChildren(bool) bool }); ok {
			return cancelResultMsg{Cancelled: scoped.CancelActiveWithChildren(children)}
		}
		return cancelResultMsg{Cancelled: runtime.CancelActive()}
	}
}

func hasActiveChildren(runtime Runtime) bool {
	if scoped, ok := runtime.(interface{ HasActiveChildren() bool }); ok {
		return scoped.HasActiveChildren()
	}
	scoped, ok := runtime.(interface{ HasActiveForegroundChildren() bool })
	return ok && scoped.HasActiveForegroundChildren()
}

func executeAction(ctx context.Context, runtime Runtime, action Action) tea.Cmd {
	return func() tea.Msg {
		actionRuntime, ok := runtime.(ActionRuntime)
		if !ok {
			return actionResultMsg{Action: action, Err: errActionUnsupported}
		}
		err := actionRuntime.ExecuteAction(ctx, action)
		return actionResultMsg{Action: action, Err: err}
	}
}

func pollBackground(processID string, generation uint64) tea.Cmd {
	return tea.Tick(750*time.Millisecond, func(time.Time) tea.Msg {
		return backgroundPollMsg{Generation: generation, ProcessID: processID}
	})
}

func refreshBackground(runtime Runtime, processID string, generation uint64) tea.Cmd {
	return func() tea.Msg {
		actionRuntime, ok := runtime.(ActionRuntime)
		if !ok {
			return backgroundPollResultMsg{Generation: generation, ProcessID: processID, Err: errActionUnsupported}
		}
		err := actionRuntime.ExecuteAction(context.Background(), Action{Kind: ActionLogsBackground, Target: processID, Offset: -1, Limit: 400})
		return backgroundPollResultMsg{Generation: generation, ProcessID: processID, Err: err}
	}
}

func shutdownApplication(runtime Runtime) tea.Cmd {
	return func() tea.Msg {
		shutdown, ok := runtime.(interface{ Shutdown(context.Context) error })
		if !ok {
			return shutdownResultMsg{}
		}
		return shutdownResultMsg{Err: shutdown.Shutdown(context.Background())}
	}
}
