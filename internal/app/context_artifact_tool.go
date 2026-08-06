package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/tool"
)

const (
	contextReadArtifactTool    = "context.read_artifact"
	artifactReadDefaultBytes   = 16 << 10
	artifactReadMaximumBytes   = 64 << 10
	artifactReadBinaryMaxBytes = 48 << 10
	artifactGrepMaximumMatches = 100
)

type contextArtifactDriver struct {
	sessionID string
	store     *session.Service
}

type artifactReadInput struct {
	ArtifactID string `json:"artifact_id"`
	Mode       string `json:"mode,omitempty"`
	Offset     int    `json:"offset,omitempty"`
	LimitBytes int    `json:"limit_bytes,omitempty"`
	StartLine  int    `json:"start_line,omitempty"`
	EndLine    int    `json:"end_line,omitempty"`
	Pattern    string `json:"pattern,omitempty"`
}

func (d *contextArtifactDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{Name: contextReadArtifactTool, Description: "Read a bounded preview, byte range, line range, tail, or grep result from a context artifact in the current session. Full reads are allowed only for artifacts up to 64 KiB.", InputSchema: tool.Schema{
		Type: "object", Properties: map[string]tool.Schema{
			"artifact_id": {Type: "string"},
			"mode":        {Type: "string", Enum: []string{"preview", "range", "line_range", "tail", "grep", "full"}},
			"offset":      {Type: "integer"}, "limit_bytes": {Type: "integer"},
			"start_line": {Type: "integer"}, "end_line": {Type: "integer"}, "pattern": {Type: "string"},
		}, Required: []string{"artifact_id"}, AdditionalProperties: &additional,
	}, EffectType: tool.EffectReadOnly, RequiresApproval: false, RequiresActionTask: false, RiskLevel: "low", Metadata: map[string]string{"approval": "allow"}, PolicyTags: []string{"session", "context", "read-only"}}
}

func (d *contextArtifactDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input artifactReadInput
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return artifactToolError(call, err), nil
	}
	limit, err := normalizeArtifactReadInput(&input)
	if err != nil {
		return artifactToolError(call, err), nil
	}
	artifact, err := d.store.LoadArtifact(ctx, d.sessionID, input.ArtifactID)
	if err != nil {
		return artifactToolError(call, err), nil
	}
	content, start, truncated, err := selectArtifactContent(artifact, input, limit)
	if err != nil {
		return artifactToolError(call, err), nil
	}
	return artifactToolResult(call, artifact, input.Mode, content, start, truncated), nil
}

func normalizeArtifactReadInput(input *artifactReadInput) (int, error) {
	if strings.TrimSpace(input.ArtifactID) == "" {
		return 0, fmt.Errorf("artifact_id is required")
	}
	if input.Mode == "" {
		input.Mode = "preview"
	}
	limit := input.LimitBytes
	if limit == 0 {
		limit = artifactReadDefaultBytes
	}
	if limit < 1 || limit > artifactReadMaximumBytes || input.Offset < 0 {
		return 0, fmt.Errorf("limit_bytes must be 1..%d and offset cannot be negative", artifactReadMaximumBytes)
	}
	return limit, nil
}

func artifactToolResult(call tool.Call, artifact session.ContextArtifact, mode string, content []byte, start int, truncated bool) tool.Result {
	encoding := "utf-8"
	if !utf8.Valid(content) {
		encoding = "base64"
		if len(content) > artifactReadBinaryMaxBytes {
			content, truncated = content[:artifactReadBinaryMaxBytes], true
		}
		content = []byte(base64.StdEncoding.EncodeToString(content))
	}
	metadata, _ := json.Marshal(map[string]any{
		"artifact_id": artifact.ID, "mode": mode, "encoding": encoding, "offset": start,
		"returned_bytes": len(content), "artifact_bytes": len(artifact.Payload), "sha256": artifact.SHA256, "truncated": truncated,
	})
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: string(content), Structured: metadata}
}

func selectArtifactContent(artifact session.ContextArtifact, input artifactReadInput, limit int) ([]byte, int, bool, error) {
	payload := artifact.Payload
	switch input.Mode {
	case "preview":
		value := []byte(artifact.Preview)
		if len(value) > limit {
			return value[:limit], 0, true, nil
		}
		return value, 0, false, nil
	case "full":
		if len(payload) > artifactReadMaximumBytes {
			return nil, 0, false, fmt.Errorf("full artifact read is limited to %d bytes; use range, line_range, tail, or grep", artifactReadMaximumBytes)
		}
		return append([]byte(nil), payload...), 0, false, nil
	case "range":
		if input.Offset > len(payload) {
			return nil, 0, false, fmt.Errorf("offset %d exceeds artifact size %d", input.Offset, len(payload))
		}
		end := min(len(payload), input.Offset+limit)
		return append([]byte(nil), payload[input.Offset:end]...), input.Offset, end < len(payload), nil
	case "tail":
		start := max(0, len(payload)-limit)
		return append([]byte(nil), payload[start:]...), start, start > 0, nil
	case "line_range":
		return selectArtifactLineRange(payload, input, limit)
	case "grep":
		return selectArtifactGrep(payload, input.Pattern, limit)
	default:
		return nil, 0, false, fmt.Errorf("unsupported artifact read mode %q", input.Mode)
	}
}

func selectArtifactLineRange(payload []byte, input artifactReadInput, limit int) ([]byte, int, bool, error) {
	if input.StartLine < 1 || input.EndLine < input.StartLine {
		return nil, 0, false, fmt.Errorf("line_range requires 1 <= start_line <= end_line")
	}
	value, start, more := artifactLineRange(payload, input.StartLine, input.EndLine, limit)
	return value, start, more, nil
}

func selectArtifactGrep(payload []byte, expression string, limit int) ([]byte, int, bool, error) {
	if strings.TrimSpace(expression) == "" || len(expression) > 512 {
		return nil, 0, false, fmt.Errorf("grep pattern must contain 1..512 bytes")
	}
	pattern, err := regexp.Compile(expression)
	if err != nil {
		return nil, 0, false, fmt.Errorf("compile grep pattern: %w", err)
	}
	value, more := artifactGrep(payload, pattern, limit)
	return value, 0, more, nil
}

func artifactLineRange(payload []byte, startLine, endLine, limit int) ([]byte, int, bool) {
	line, offset, first := 1, 0, -1
	var out bytes.Buffer
	for offset < len(payload) && line <= endLine {
		next := bytes.IndexByte(payload[offset:], '\n')
		if next < 0 {
			next = len(payload) - offset
		}
		end := offset + next
		if end < len(payload) {
			end++
		}
		if line >= startLine {
			if first < 0 {
				first = offset
			}
			remaining := limit - out.Len()
			if end-offset > remaining {
				out.Write(payload[offset : offset+remaining])
				return out.Bytes(), first, true
			}
			out.Write(payload[offset:end])
		}
		offset, line = end, line+1
	}
	if first < 0 {
		first = len(payload)
	}
	return out.Bytes(), first, offset < len(payload) && line <= endLine
}

func artifactGrep(payload []byte, pattern *regexp.Regexp, limit int) ([]byte, bool) {
	line, offset, matches := 1, 0, 0
	var out bytes.Buffer
	for offset < len(payload) {
		next := bytes.IndexByte(payload[offset:], '\n')
		if next < 0 {
			next = len(payload) - offset
		}
		value := payload[offset : offset+next]
		if pattern.Match(value) {
			formatted := fmt.Sprintf("%d:%s\n", line, strings.ToValidUTF8(string(value), "�"))
			if out.Len()+len(formatted) > limit || matches == artifactGrepMaximumMatches {
				return out.Bytes(), true
			}
			out.WriteString(formatted)
			matches++
		}
		offset += next + 1
		line++
	}
	return out.Bytes(), false
}

func artifactToolError(call tool.Call, err error) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: err.Error(), IsError: true}
}
