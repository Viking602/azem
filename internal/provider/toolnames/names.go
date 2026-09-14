// Package toolnames preserves local tool identities across provider wire restrictions.
package toolnames

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/Viking602/venat/message"
)

// Names maps Azem/Venat tool identifiers onto provider-safe wire names.
// Many OpenAI-compatible APIs (including DeepSeek) reject names that do not
// match ^[a-zA-Z0-9_-]+$, while Venat coding tools use dotted names such as
// coding.read_file.
type Names struct {
	toWire  map[string]string
	toLocal map[string]string
}

func New(definitions []message.ToolDefinition) *Names {
	names := &Names{
		toWire:  make(map[string]string, len(definitions)),
		toLocal: make(map[string]string, len(definitions)),
	}
	for _, definition := range definitions {
		names.register(definition.Name)
	}
	return names
}

// Alias keeps an existing tool's identity while preferring a native provider
// name. An advertised tool already owning that name always wins.
func (n *Names) Alias(local, preferred string) bool {
	if n == nil || preferred == "" || Sanitize(preferred) != preferred {
		return false
	}
	if _, exists := n.toLocal[preferred]; exists {
		return false
	}
	previous, exists := n.toWire[local]
	if !exists {
		return false
	}
	delete(n.toLocal, previous)
	n.toWire[local], n.toLocal[preferred] = preferred, local
	return true
}

func (n *Names) register(local string) string {
	if n == nil {
		return Sanitize(local)
	}
	local = strings.TrimSpace(local)
	if local == "" {
		return local
	}
	if wire, ok := n.toWire[local]; ok {
		return wire
	}
	base := Sanitize(local)
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

func (n *Names) Wire(local string) string {
	if n == nil {
		return Sanitize(local)
	}
	if wire, ok := n.toWire[local]; ok {
		return wire
	}
	return n.register(local)
}

func (n *Names) Local(wire string) string {
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

// Sanitize rewrites a tool name to the OpenAI/DeepSeek function-name
// pattern ^[a-zA-Z0-9_-]+$. Non-matching runes become underscores.
func Sanitize(name string) string {
	if name == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(name))
	lastUnderscore := false
	for _, runeValue := range name {
		if runeValue <= unicode.MaxASCII && (unicode.IsLetter(runeValue) || unicode.IsDigit(runeValue) || runeValue == '_' || runeValue == '-') {
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
