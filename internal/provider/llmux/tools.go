package llmuxdriver

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/Viking602/venat/message"
)

// toolNames maps Azem/Venat tool identifiers onto provider-safe wire names.
// Many OpenAI-compatible APIs (including DeepSeek) reject names that do not
// match ^[a-zA-Z0-9_-]+$, while Venat coding tools use dotted names such as
// coding.read_file.
type toolNames struct {
	toWire  map[string]string
	toLocal map[string]string
}

func newToolNames(definitions []message.ToolDefinition) *toolNames {
	names := &toolNames{
		toWire:  make(map[string]string, len(definitions)),
		toLocal: make(map[string]string, len(definitions)),
	}
	for _, definition := range definitions {
		names.register(definition.Name)
	}
	return names
}

func (n *toolNames) register(local string) string {
	if n == nil {
		return sanitizeToolName(local)
	}
	local = strings.TrimSpace(local)
	if local == "" {
		return local
	}
	if wire, ok := n.toWire[local]; ok {
		return wire
	}
	base := sanitizeToolName(local)
	if base == "" {
		base = "tool"
	}
	wire := base
	for index := 2; ; index++ {
		if existing, taken := n.toLocal[wire]; !taken || existing == local {
			break
		}
		wire = fmt.Sprintf("%s_%d", base, index)
	}
	n.toWire[local] = wire
	n.toLocal[wire] = local
	return wire
}

func (n *toolNames) Wire(local string) string {
	if n == nil {
		return sanitizeToolName(local)
	}
	if wire, ok := n.toWire[local]; ok {
		return wire
	}
	return n.register(local)
}

func (n *toolNames) Local(wire string) string {
	if n == nil {
		return wire
	}
	wire = strings.TrimSpace(wire)
	if wire == "" {
		return wire
	}
	if local, ok := n.toLocal[wire]; ok {
		return local
	}
	return wire
}

// sanitizeToolName rewrites a tool name to the OpenAI/DeepSeek function-name
// pattern ^[a-zA-Z0-9_-]+$. Non-matching runes become underscores.
func sanitizeToolName(name string) string {
	if name == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(name))
	lastUnderscore := false
	for _, runeValue := range name {
		if unicode.IsLetter(runeValue) || unicode.IsDigit(runeValue) || runeValue == '_' || runeValue == '-' {
			builder.WriteRune(runeValue)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(builder.String(), "_")
}
