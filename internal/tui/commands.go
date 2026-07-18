package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

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
	ActionReconcileAttempt = app.ActionReconcileAttempt
	ActionInspectAgent     = app.ActionInspectAgent
	ActionListAgentTypes   = app.ActionListAgentTypes
	ActionListPersonas     = app.ActionListPersonas
	ActionCancelAgent      = app.ActionCancelAgent
	ActionRefreshMCP       = app.ActionRefreshMCP
	ActionReconnectMCP     = app.ActionReconnectMCP
	ActionListSkills       = app.ActionListSkills
	ActionReloadSkills     = app.ActionReloadSkills
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
	{Name: "models", Usage: "/models", Detail: "Search and choose the active model"},
	{Name: "skills", Usage: "/skills [reload]", Detail: "Inspect or reload Agent Skills"},
	{Name: "skill", Usage: "/skill <name> [instruction]", Detail: "Run one turn with an active Agent Skill"},
	{Name: "provider", Usage: "/provider [chatgpt|grok]", Detail: "Switch the active provider"},
	{Name: "reasoning", Usage: "/reasoning [level]", Detail: "Set reasoning effort"},
	{Name: "login", Usage: "/login [chatgpt|grok]", Detail: "Sign in to a subscription provider"},
	{Name: "logout", Usage: "/logout [chatgpt|grok]", Detail: "Sign out of a provider"},
	{Name: "team", Usage: "/team on|off", Detail: "Toggle coding-team mode"},
	{Name: "agents", Usage: "/agents [cancel <id>]", Detail: "Inspect or cancel child agents"},
	{Name: "agent-types", Usage: "/agent-types", Detail: "Inspect configured child agent types"},
	{Name: "personas", Usage: "/personas", Detail: "Inspect configured child personas"},
	{Name: "new", Usage: "/new", Detail: "Start a new session"},
	{Name: "sessions", Usage: "/sessions", Detail: "List saved sessions"},
	{Name: "resume", Usage: "/resume", Detail: "Choose a saved session"},
	{Name: "compact", Usage: "/compact", Detail: "Compact the current session"},
	{Name: "mcp", Usage: "/mcp [refresh|reconnect <server>]", Detail: "Inspect or update MCP servers"},
	{Name: "reconcile", Usage: "/reconcile <attempt-id> <result>", Detail: "Resolve an unknown side effect"},
	{Name: "cancel", Usage: "/cancel", Detail: "Cancel the active run"},
	{Name: "help", Usage: "/help", Detail: "Open keyboard and command help"},
	{Name: "quit", Usage: "/quit", Detail: "Quit Azem"},
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
	return Command{Name: strings.ToLower(fields[0]), Args: fields[1:]}, true, nil
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

func hasActiveForegroundChildren(runtime Runtime) bool {
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

func shutdownApplication(runtime Runtime) tea.Cmd {
	return func() tea.Msg {
		shutdown, ok := runtime.(interface{ Shutdown(context.Context) error })
		if !ok {
			return shutdownResultMsg{}
		}
		return shutdownResultMsg{Err: shutdown.Shutdown(context.Background())}
	}
}
