package desktopipc

import (
	"fmt"
	"strings"

	"github.com/Viking602/azem/internal/desktop"
	"github.com/Viking602/azem/internal/githubpr"
)

func BridgeEmitter(hub *EventHub) desktop.EventEmitter {
	return func(name string, values ...any) bool {
		if hub == nil || len(values) == 0 {
			return false
		}
		switch name {
		case desktop.EventName:
			event, ok := values[0].(desktop.Event)
			if !ok {
				return false
			}
			replaceKey, lossless := runtimeEventPolicy(event)
			_, err := hub.Publish(ChannelRuntime, event, replaceKey, lossless)
			return err == nil
		case desktop.PullRequestEventName:
			state, ok := values[0].(githubpr.MonitorState)
			if !ok {
				return false
			}
			_, err := hub.Publish(ChannelPullRequest, state, fmt.Sprintf("pr:%d", state.Number), false)
			return err == nil
		case desktop.TerminalEventName:
			event, ok := values[0].(desktop.TerminalEvent)
			if !ok {
				return false
			}
			if event.Kind == "output" {
				return true
			}
			_, err := hub.Publish(ChannelTerminal, event, "", true)
			return err == nil
		default:
			return false
		}
	}
}

func runtimeEventPolicy(event desktop.Event) (string, bool) {
	switch event.Kind {
	case "text_delta", "thinking_delta":
		// Text is incremental. Dropping or replacing a delta corrupts the
		// transcript; the app broker already concatenates bursts upstream.
		return "", true
	case "context_usage", "context_profile", "tool_update", "agent_state", "background_logs":
		parts := []string{event.Kind, event.SessionID, event.RunID, event.AgentID, event.ToolCallID, event.State}
		return strings.Join(parts, ":"), false
	default:
		return "", true
	}
}
