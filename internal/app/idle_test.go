package app

import (
	agentservice "github.com/Viking602/azem/internal/agent"
	"testing"
)

func TestHasActiveWorkIncludesDetachedChildren(t *testing.T) {
	child := &activeSubagent{}
	children := &subagentRuntime{active: map[string]*activeSubagent{"child": child}}
	service := &Service{providers: &ProviderRuntime{subagents: children}}
	for _, tc := range []struct {
		name, state                       string
		terminalizing, terminalized, want bool
	}{
		{"running", "running", false, false, true},
		{"queued", "queued", false, false, true},
		{"settling", "completed", true, false, true},
		{"completed", "completed", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			child.run = agentservice.SubagentRun{State: agentservice.SubagentState(tc.state)}
			child.terminalizing, child.terminalized = tc.terminalizing, tc.terminalized
			if got := service.HasActiveWork(); got != tc.want {
				t.Fatalf("active = %v, want %v", got, tc.want)
			}
		})
	}
	children.wakeInFlight = map[string]bool{"child": true}
	if !service.HasActiveWork() {
		t.Fatal("ignored pending child wake")
	}
	delete(children.wakeInFlight, "child")
	service.activeRun = "starting"
	if !service.HasActiveWork() {
		t.Fatal("ignored starting main run")
	}
	service.activeRun = ""
	if service.HasActiveWork() {
		t.Fatal("completed work kept daemon alive")
	}
}
