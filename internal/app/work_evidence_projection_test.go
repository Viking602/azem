package app

import (
	"encoding/json"
	"strings"
	"testing"

	agentservice "github.com/Viking602/azem/internal/agent"
)

func TestSubagentStateEventProjectsOnlyDurableEvidenceStatus(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{"": "", "provisional": "provisional", "verified": "verified", "stale": "stale", "untrusted": ""} {
		event := subagentStateEvent(agentservice.SubagentRun{ID: "child", SessionID: "session", EvidenceStatus: input}, "")
		if event.Kind != EventAgentState || event.Agent == nil || event.Agent.EvidenceStatus != want {
			t.Fatalf("input %q event = %+v", input, event)
		}
		encoded, err := json.Marshal(event.Agent)
		if err != nil {
			t.Fatal(err)
		}
		if want == "" {
			if strings.Contains(string(encoded), `"evidenceStatus":`) {
				t.Fatalf("missing evidence producer was projected as status: %s", encoded)
			}
			continue
		}
		if !strings.Contains(string(encoded), `"evidenceStatus":"`+want+`"`) {
			t.Fatalf("encoded payload = %s", encoded)
		}
	}
}
