package app

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/Viking602/venat/tool"
)

const maxAgentToolResultContentBytes = 96 << 10

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
