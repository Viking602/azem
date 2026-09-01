package agentruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/orchestration"
)

var ErrTeamExecutionSuspended = errors.New("team execution suspended")

// TeamAgentClass is Azem's persistent role definition. Venat orchestration only
// receives the executable Agent request built from it.
type TeamAgentClass struct {
	Name            string             `json:"name"`
	Description     string             `json:"description,omitempty"`
	Instructions    string             `json:"instructions,omitempty"`
	Skills          []string           `json:"skills,omitempty"`
	AvailableSkills []string           `json:"availableSkills,omitempty"`
	Model           string             `json:"model,omitempty"`
	Tools           []string           `json:"tools,omitempty"`
	InputSchema     json.RawMessage    `json:"inputSchema,omitempty"`
	OutputSchema    json.RawMessage    `json:"outputSchema,omitempty"`
	LoopPolicy      hyagent.LoopPolicy `json:"loopPolicy,omitempty"`
	Capabilities    []Capability       `json:"capabilities,omitempty"`
}

func (class TeamAgentClass) Spec() hyagent.Spec {
	return hyagent.Spec{
		Instructions: class.Instructions, Skills: class.Skills, AvailableSkills: class.AvailableSkills,
		Model: class.Model, Tools: class.Tools, LoopPolicy: class.LoopPolicy,
	}
}

type TeamInstanceState string

const (
	TeamInstancePending  TeamInstanceState = "pending"
	TeamInstanceRunning  TeamInstanceState = "running"
	TeamInstanceFinished TeamInstanceState = "finished"
	TeamInstanceFailed   TeamInstanceState = "failed"
)

type TeamInstance struct {
	ID             string            `json:"id"`
	ClassName      string            `json:"className"`
	AgentClassName string            `json:"agentClassName,omitempty"`
	RunID          string            `json:"runId"`
	TaskID         string            `json:"taskId,omitempty"`
	State          TeamInstanceState `json:"state"`
	CreatedAt      time.Time         `json:"createdAt"`
}

type TeamHandoff struct {
	ID                   string          `json:"id"`
	RunID                string          `json:"runId"`
	From                 string          `json:"from"`
	To                   string          `json:"to"`
	Reason               string          `json:"reason,omitempty"`
	Payload              json.RawMessage `json:"payload,omitempty"`
	EvidenceIDs          []string        `json:"evidenceIds,omitempty"`
	RequiredOutputSchema json.RawMessage `json:"requiredOutputSchema,omitempty"`
	CreatedAt            time.Time       `json:"createdAt"`
}

type TeamDispatch struct {
	To             string               `json:"to"`
	ClassName      string               `json:"className,omitempty"`
	AgentClassName string               `json:"agentClassName,omitempty"`
	Task           Task                 `json:"task"`
	Input          json.RawMessage      `json:"input,omitempty"`
	OutputPolicy   hyagent.OutputPolicy `json:"outputPolicy,omitempty"`
	Handoff        *TeamHandoff         `json:"handoff,omitempty"`
	Skip           bool                 `json:"skip,omitempty"`
}

func ValidateTeamDispatch(dispatch TeamDispatch) error {
	if dispatch.Handoff != nil {
		if dispatch.Handoff.RunID != "" && dispatch.Handoff.RunID != dispatch.Task.RunID {
			return fmt.Errorf("team dispatch: handoff run %q does not match task run %q", dispatch.Handoff.RunID, dispatch.Task.RunID)
		}
		if dispatch.Handoff.To != "" && dispatch.Handoff.To != dispatch.To {
			return fmt.Errorf("team dispatch: handoff target %q does not match dispatch target %q", dispatch.Handoff.To, dispatch.To)
		}
	}
	inputs := []json.RawMessage{dispatch.Task.Input, dispatch.Input}
	if dispatch.Handoff != nil {
		inputs = append(inputs, dispatch.Handoff.Payload)
	}
	var input json.RawMessage
	for _, candidate := range inputs {
		if len(candidate) == 0 {
			continue
		}
		if !json.Valid(candidate) {
			return fmt.Errorf("team dispatch: input is not valid JSON")
		}
		if len(input) == 0 {
			input = candidate
			continue
		}
		var left, right bytes.Buffer
		if json.Compact(&left, input) != nil || json.Compact(&right, candidate) != nil || !bytes.Equal(left.Bytes(), right.Bytes()) {
			return fmt.Errorf("team dispatch: task, dispatch, and handoff inputs do not match")
		}
	}
	if err := hyagent.ValidateJSON(dispatch.Task.InputSchema, input); err != nil {
		return fmt.Errorf("team dispatch input: %w", err)
	}
	return nil
}

// TeamState preserves legacy application facts while adding the v0.16
// mechanical orchestration snapshot used for new executions.
type TeamState struct {
	RunID         string              `json:"runId"`
	Tick          int                 `json:"tick,omitempty"`
	Tasks         []Task              `json:"tasks,omitempty"`
	Instances     []TeamInstance      `json:"instances,omitempty"`
	Blackboard    []BlackboardItem    `json:"blackboard,omitempty"`
	Orchestration orchestration.State `json:"orchestration,omitempty"`
}

type TeamExecutionResult struct {
	State TeamState `json:"state"`
	Ticks int       `json:"ticks"`
}

func ComputeTeamInstanceID(className, runID, taskID, suffix string) string {
	parts := []string{
		strings.TrimSpace(className), strings.TrimSpace(runID),
		strings.TrimSpace(taskID), strings.TrimSpace(suffix),
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return "ai-" + hex.EncodeToString(digest[:8])
}
