package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Viking602/azem/internal/securityscan"
	"github.com/Viking602/venat/tool"
)

const (
	securityProgressTool        = "security.record_progress"
	securitySubmitDraftTool     = "security.submit_draft"
	securityReducerInputsTool   = "security.reducer_inputs"
	securitySubmitReductionTool = "security.submit_reduction"
	securitySubmitMatchesTool   = "security.submit_matches"
	securitySubmitPatchTool     = "security.submit_patch_result"
	securitySubmitVerifyTool    = "security.submit_verification"
)

type securityProgressInput struct {
	Phase         securityscan.Phase `json:"phase"`
	ReviewedPaths []string           `json:"reviewedPaths"`
	Message       string             `json:"message,omitempty"`
}

type securityDraftInput struct {
	Draft json.RawMessage `json:"draft"`
}

type securityProgressDriver struct {
	service  *securityscan.Service
	scanID   string
	workerID string
}

func (d *securityProgressDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name: securityProgressTool, Description: "Record a real forward security-scan phase transition or completed source-review batch. Every reviewed path must be repository-relative and inside the host inventory.",
		InputSchema: tool.Schema{Type: "object", Properties: map[string]tool.Schema{
			"phase":         {Type: "string", Enum: []string{"threat_model", "discovery", "validation", "attack_path", "reporting"}},
			"reviewedPaths": {Type: "array", Items: &tool.Schema{Type: "string"}},
			"message":       {Type: "string"},
		}, Required: []string{"phase", "reviewedPaths"}, AdditionalProperties: &additional},
	}
}

func (d *securityProgressDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input securityProgressInput
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return securityToolError(call, "decode progress: "+err.Error()), nil
	}
	if err := d.service.RecordProgress(ctx, d.scanID, d.workerID, input.Phase, input.ReviewedPaths, input.Message); err != nil {
		return securityToolError(call, err.Error()), nil
	}
	payload, _ := json.Marshal(map[string]any{"accepted": true, "reviewedPaths": len(input.ReviewedPaths), "phase": input.Phase})
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "Security scan progress recorded.", Structured: payload}, nil
}

type securitySubmitDriver struct {
	service  *securityscan.Service
	scanID   string
	workerID string
	name     string
}

func (d *securitySubmitDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name: d.name, Description: "Submit one complete semantic security-scan draft for the current host-bound worker. The host validates the contract and derives all stable identities.",
		InputSchema: tool.Schema{Type: "object", Properties: map[string]tool.Schema{
			"draft": {Type: "object"},
		}, Required: []string{"draft"}, AdditionalProperties: &additional},
	}
}

func (d *securitySubmitDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input securityDraftInput
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return securityToolError(call, "decode draft: "+err.Error()), nil
	}
	var draft securityscan.Draft
	if err := json.Unmarshal(input.Draft, &draft); err != nil {
		return securityToolError(call, "decode semantic draft: "+err.Error()), nil
	}
	if err := d.service.SubmitDraft(ctx, d.scanID, d.workerID, draft); err != nil {
		return securityToolError(call, err.Error()), nil
	}
	payload, _ := json.Marshal(map[string]any{"accepted": true, "scanId": d.scanID, "workerId": d.workerID})
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "Semantic security draft accepted. Do not submit it again.", Structured: payload}, nil
}

type securityReducerInputsDriver struct {
	service  *securityscan.Service
	scanID   string
	workerID string
}

func (d *securityReducerInputsDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name: securityReducerInputsTool, Description: "Read the previous aggregate and newly completed Standard scan drafts assigned to this reducer.",
		InputSchema: tool.Schema{Type: "object", AdditionalProperties: &additional},
	}
}

func (d *securityReducerInputsDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	inputs, err := d.service.ReducerInputs(d.scanID, d.workerID)
	if err != nil {
		return securityToolError(call, err.Error()), nil
	}
	payload, err := json.Marshal(map[string]any{"scanId": d.scanID, "drafts": inputs})
	if err != nil {
		return securityToolError(call, err.Error()), nil
	}
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: fmt.Sprintf("Loaded %d reducer inputs.", len(inputs)), Structured: payload}, nil
}

type securitySubmitMatchesDriver struct {
	service  *securityscan.Service
	scanID   string
	workerID string
}

func (d *securitySubmitMatchesDriver) Definition() tool.Definition {
	additional := false
	pair := tool.Schema{Type: "object", Properties: map[string]tool.Schema{
		"beforeOccurrenceId": {Type: "string"}, "afterOccurrenceId": {Type: "string"}, "confidence": {Type: "number"},
	}, Required: []string{"beforeOccurrenceId", "afterOccurrenceId", "confidence"}, AdditionalProperties: &additional}
	return tool.Definition{
		Name: securitySubmitMatchesTool, Description: "Submit one-to-one semantic matches for the host-assigned unmatched finding sets.",
		InputSchema: tool.Schema{Type: "object", Properties: map[string]tool.Schema{"pairs": {Type: "array", Items: &pair}}, Required: []string{"pairs"}, AdditionalProperties: &additional},
	}
}

func (d *securitySubmitMatchesDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		Pairs []securityscan.MatchPair `json:"pairs"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return securityToolError(call, err.Error()), nil
	}
	if err := d.service.SubmitMatches(d.scanID, d.workerID, input.Pairs); err != nil {
		return securityToolError(call, err.Error()), nil
	}
	payload, _ := json.Marshal(map[string]any{"accepted": true, "matches": len(input.Pairs)})
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "Semantic finding matches accepted.", Structured: payload}, nil
}

type securitySubmitPatchDriver struct {
	service  *securityscan.Service
	scanID   string
	workerID string
	name     string
}

func (d *securitySubmitPatchDriver) Definition() tool.Definition {
	additional := false
	statuses := []string{"generated", "no_change", "blocked", "failed"}
	if d.name == securitySubmitVerifyTool {
		statuses = []string{"verified", "still_vulnerable", "inconclusive"}
	}
	return tool.Definition{
		Name: d.name, Description: "Submit the complete host-bound patch or independent verification result.",
		InputSchema: tool.Schema{Type: "object", Properties: map[string]tool.Schema{
			"occurrenceId": {Type: "string"}, "status": {Type: "string", Enum: statuses},
			"files":        {Type: "array", Items: &tool.Schema{Type: "string"}},
			"verification": {Type: "string"}, "reason": {Type: "string"},
		}, Required: []string{"occurrenceId", "status", "files"}, AdditionalProperties: &additional},
	}
}

func (d *securitySubmitPatchDriver) Execute(_ context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var result securityscan.PatchResult
	if err := json.Unmarshal(call.Arguments, &result); err != nil {
		return securityToolError(call, err.Error()), nil
	}
	if err := d.service.SubmitPatchResult(d.scanID, d.workerID, result); err != nil {
		return securityToolError(call, err.Error()), nil
	}
	payload, _ := json.Marshal(map[string]any{"accepted": true, "status": result.Status})
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "Security remediation result accepted.", Structured: payload}, nil
}

func securityDrivers(service *securityscan.Service, request securityscan.ExecutionRequest) []tool.Driver {
	switch request.Worker.Kind {
	case securityscan.WorkerReducer:
		return []tool.Driver{
			&securityReducerInputsDriver{service: service, scanID: request.Scan.ID, workerID: request.Worker.ID},
			&securitySubmitDriver{service: service, scanID: request.Scan.ID, workerID: request.Worker.ID, name: securitySubmitReductionTool},
		}
	case securityscan.WorkerMatcher:
		return []tool.Driver{&securitySubmitMatchesDriver{service: service, scanID: request.Scan.ID, workerID: request.Worker.ID}}
	case securityscan.WorkerFixer:
		return []tool.Driver{&securitySubmitPatchDriver{service: service, scanID: request.Scan.ID, workerID: request.Worker.ID, name: securitySubmitPatchTool}}
	case securityscan.WorkerVerifier:
		return []tool.Driver{&securitySubmitPatchDriver{service: service, scanID: request.Scan.ID, workerID: request.Worker.ID, name: securitySubmitVerifyTool}}
	default:
		return []tool.Driver{
			&securityProgressDriver{service: service, scanID: request.Scan.ID, workerID: request.Worker.ID},
			&securitySubmitDriver{service: service, scanID: request.Scan.ID, workerID: request.Worker.ID, name: securitySubmitDraftTool},
		}
	}
}

func securityToolError(call tool.Call, message string) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: message, IsError: true}
}
