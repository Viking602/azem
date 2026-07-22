package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"unicode/utf8"

	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/go-hydaelyn/tool"
)

const contextReadArtifactTool = "context.read_artifact"

type contextArtifactDriver struct {
	sessionID string
	store     *session.Service
}

func (d *contextArtifactDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{Name: contextReadArtifactTool, Description: "Read the exact content of a context artifact referenced by compacted history. Artifacts are restricted to the current session.", InputSchema: tool.Schema{
		Type: "object", Properties: map[string]tool.Schema{"artifact_id": {Type: "string"}}, Required: []string{"artifact_id"}, AdditionalProperties: &additional,
	}, EffectType: tool.EffectReadOnly, RequiresApproval: false, RequiresActionTask: false, RiskLevel: "low", Metadata: map[string]string{"approval": "allow"}, PolicyTags: []string{"session", "context", "read-only"}}
}

func (d *contextArtifactDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		ArtifactID string `json:"artifact_id"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: err.Error(), IsError: true}, nil
	}
	artifact, err := d.store.LoadArtifact(ctx, d.sessionID, input.ArtifactID)
	if err != nil {
		return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: err.Error(), IsError: true}, nil
	}
	encoding := "utf-8"
	content := string(artifact.Payload)
	if !utf8.Valid(artifact.Payload) {
		encoding = "base64"
		content = base64.StdEncoding.EncodeToString(artifact.Payload)
	}
	structured, _ := json.Marshal(map[string]string{"encoding": encoding, "content": content})
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: content, Structured: structured}, nil
}
