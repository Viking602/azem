package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/tool"
)

const (
	maxAgentToolResultContentBytes = 96 << 10
	// spillArtifactKind marks session context artifacts created by spilling an
	// oversized tool result out of the model-visible content.
	spillArtifactKind = "tool_result_spill"
	// spillPreviewBytes bounds the durable artifact preview column.
	spillPreviewBytes = 512
)

// spillAgentToolResult persists an oversized tool result as a session context
// artifact and replaces the truncated tail with an artifact locator, so the
// model can retrieve the full output through context.read_artifact instead of
// re-running the tool. When the artifact store is unavailable or the write
// fails, the result falls back to the plain lossy truncation.
func spillAgentToolResult(ctx context.Context, sessions *session.Service, sessionID, runID string, result tool.Result) tool.Result {
	originalContentBytes := len(result.Content)
	originalStructuredBytes := len(result.Structured)
	if originalContentBytes <= maxAgentToolResultContentBytes && originalStructuredBytes <= maxAgentToolResultContentBytes {
		return result
	}
	if sessions == nil {
		return boundAgentToolResult(result)
	}

	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()

	if originalContentBytes > maxAgentToolResultContentBytes {
		artifact, err := sessions.PutArtifact(
			persistCtx, sessionID, runID, spillArtifactKind,
			[]byte(result.Content), utf8Prefix(result.Content, spillPreviewBytes),
		)
		if err != nil {
			return boundAgentToolResult(result)
		}
		const reserve = 512
		prefix := utf8Prefix(result.Content, maxAgentToolResultContentBytes-reserve)
		result.Content = fmt.Sprintf(
			"%s\n\n[Tool result spilled by Azem: showing %d of %d content bytes; full output: artifact:%s — read slices with context.read_artifact (modes range/line_range/tail/grep).]",
			prefix, len(prefix), originalContentBytes, artifact.ID,
		)
	}
	if originalStructuredBytes > maxAgentToolResultContentBytes {
		artifact, err := sessions.PutArtifact(
			persistCtx, sessionID, runID, spillArtifactKind,
			[]byte(result.Structured), utf8Prefix(string(result.Structured), spillPreviewBytes),
		)
		receipt := map[string]any{
			"truncated":                 true,
			"original_content_bytes":    originalContentBytes,
			"original_structured_bytes": originalStructuredBytes,
			"guidance":                  "narrow the path, glob, or query",
		}
		if err == nil {
			receipt["artifact_id"] = artifact.ID
			receipt["guidance"] = "read the full structured payload with context.read_artifact"
		}
		result.Structured, _ = json.Marshal(receipt)
	}
	return result
}

// boundAgentToolResult prevents a single broad search or generated-file line
// from being copied into every execution checkpoint. The model keeps a useful
// prefix and an explicit truncation receipt; small tool results remain exact.
func boundAgentToolResult(result tool.Result) tool.Result {
	originalContentBytes := len(result.Content)
	originalStructuredBytes := len(result.Structured)
	if originalContentBytes <= maxAgentToolResultContentBytes && originalStructuredBytes <= maxAgentToolResultContentBytes {
		return result
	}

	if originalContentBytes > maxAgentToolResultContentBytes {
		const reserve = 256
		prefix := utf8Prefix(result.Content, maxAgentToolResultContentBytes-reserve)
		result.Content = fmt.Sprintf(
			"%s\n\n[Tool result truncated by Azem: showing %d of %d content bytes; narrow the path, glob, or query.]",
			prefix, len(prefix), originalContentBytes,
		)
	}
	if originalStructuredBytes > maxAgentToolResultContentBytes {
		result.Structured, _ = json.Marshal(map[string]any{
			"truncated":                 true,
			"original_content_bytes":    originalContentBytes,
			"original_structured_bytes": originalStructuredBytes,
			"guidance":                  "narrow the path, glob, or query",
		})
	}
	return result
}

func utf8Prefix(value string, limit int) string {
	if limit <= 0 || value == "" {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}
