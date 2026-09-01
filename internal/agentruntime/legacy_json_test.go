package agentruntime

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Viking602/venat/message"
)

func TestLegacyRunAndTaskStatusJSONRoundTrip(t *testing.T) {
	for _, status := range []RunStatus{
		RunStatusCompleted,
		RunStatusFailed,
		RunStatusBlocked,
		RunStatusCancelled,
		RunStatusReconcileRequired,
	} {
		t.Run("run_"+string(status), func(t *testing.T) {
			input := []byte(fmt.Sprintf(`{"id":"legacy-run","status":%q,"rootTaskId":"root","agentVersion":"v0.15.4","createdAt":"2026-01-02T03:04:05Z","updatedAt":"2026-01-02T03:05:05Z"}`, status))
			var run Run
			if err := json.Unmarshal(input, &run); err != nil {
				t.Fatal(err)
			}
			if run.Status != status || run.RootTaskID != "root" || run.AgentVersion != "v0.15.4" {
				t.Fatalf("decoded legacy run = %#v", run)
			}
			assertJSONStatus(t, run, string(status))
		})
	}

	for _, status := range []TaskStatus{
		TaskStatusCompleted,
		TaskStatusFailed,
		TaskStatusCancelled,
		TaskStatusReconcileRequired,
	} {
		t.Run("task_"+string(status), func(t *testing.T) {
			input := []byte(fmt.Sprintf(`{"taskId":"legacy-task","runId":"legacy-run","type":"worker","status":%q,"version":7,"createdAt":"2026-01-02T03:04:05Z","updatedAt":"2026-01-02T03:05:05Z"}`, status))
			var task Task
			if err := json.Unmarshal(input, &task); err != nil {
				t.Fatal(err)
			}
			if task.Status != status || task.ID != "legacy-task" || task.Version != 7 {
				t.Fatalf("decoded legacy task = %#v", task)
			}
			assertJSONStatus(t, task, string(status))
		})
	}
}

func TestLegacyTeamHandoffAndApprovalJSONDecodes(t *testing.T) {
	const input = `{
		"team":{"runId":"legacy-run","tick":4,"instances":[{"id":"instance-1","className":"reviewer","runId":"legacy-run","taskId":"task-1","state":"finished","createdAt":"2026-01-02T03:04:05Z"}]},
		"handoff":{"handoffId":"handoff-1","runId":"legacy-run","taskId":"task-1","fromAgentId":"planner","toAgentId":"reviewer","reason":"verify","contextReferences":["artifact-1"],"taskVersion":3},
		"approval":{"approvalId":"approval-1","runId":"legacy-run","taskId":"task-1","actionId":"action-1","requesterAgentId":"reviewer","status":"pending","requestedAction":"publish"},
		"decision":{"approvalId":"approval-1","decidedBy":"operator","decision":"deny","reason":"unsafe","decidedAt":"2026-01-02T03:06:05Z"}
	}`
	var legacy struct {
		Team     TeamState        `json:"team"`
		Handoff  HandoffRequest   `json:"handoff"`
		Approval ApprovalRequest  `json:"approval"`
		Decision ApprovalDecision `json:"decision"`
	}
	if err := json.Unmarshal([]byte(input), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Team.RunID != "legacy-run" || legacy.Team.Tick != 4 || len(legacy.Team.Instances) != 1 || legacy.Team.Instances[0].State != TeamInstanceFinished {
		t.Fatalf("decoded legacy team = %#v", legacy.Team)
	}
	if legacy.Handoff.HandoffID != "handoff-1" || legacy.Handoff.ToAgentID != "reviewer" || legacy.Handoff.TaskVersion != 3 {
		t.Fatalf("decoded legacy handoff = %#v", legacy.Handoff)
	}
	if legacy.Approval.Status != "pending" || legacy.Approval.RequestedAction != "publish" {
		t.Fatalf("decoded legacy approval = %#v", legacy.Approval)
	}
	if legacy.Decision.Decision != "deny" || legacy.Decision.DecidedBy != "operator" {
		t.Fatalf("decoded legacy decision = %#v", legacy.Decision)
	}
}

func TestUnmarshalMessagesPreservesLegacyEmbeddedRuntimeMetadata(t *testing.T) {
	createdAt := time.Date(2026, time.August, 30, 1, 2, 3, 4, time.UTC)
	legacy := message.NewText(message.RoleAssistant, "trusted private context")
	SetMessageIdentity(&legacy, "team-1", "agent-1", "run-1", "parent-1")
	SetMessageVisibility(&legacy, MessageVisibilityPrivate)
	SetMessageCreatedAt(&legacy, createdAt)
	encoded, err := json.Marshal([]message.Message{legacy})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalMessages(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 || MessageVisibilityOf(decoded[0]) != MessageVisibilityPrivate ||
		MessageTeamID(decoded[0]) != "team-1" || MessageAgentID(decoded[0]) != "agent-1" ||
		MessageRunID(decoded[0]) != "run-1" || MessageParentRunID(decoded[0]) != "parent-1" ||
		!MessageCreatedAt(decoded[0]).Equal(createdAt) {
		t.Fatalf("decoded legacy message = %#v", decoded)
	}
}

func assertJSONStatus(t *testing.T, value any, want string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := json.Unmarshal(object["status"], &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round-trip status = %q, want %q; JSON=%s", got, want, encoded)
	}
}
