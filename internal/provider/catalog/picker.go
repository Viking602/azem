package catalog

import (
	"fmt"
	"strings"
)

type Picker struct {
	Catalog Result
	Cursor  int
}

func NewPicker(catalog Result, selectedModel string) (Picker, error) {
	if len(catalog.Models) == 0 {
		return Picker{}, fmt.Errorf("model catalog is empty")
	}
	picker := Picker{Catalog: catalog}
	for index, model := range catalog.Models {
		if model.ID == selectedModel {
			picker.Cursor = index
			break
		}
	}
	return picker, nil
}

func (p *Picker) Move(delta int) {
	if len(p.Catalog.Models) == 0 {
		return
	}
	p.Cursor = (p.Cursor + delta) % len(p.Catalog.Models)
	if p.Cursor < 0 {
		p.Cursor += len(p.Catalog.Models)
	}
}

func (p Picker) Current() (Model, bool) {
	if p.Cursor < 0 || p.Cursor >= len(p.Catalog.Models) {
		return Model{}, false
	}
	return p.Catalog.Models[p.Cursor], true
}

func (p Picker) Select(provider string, accountID string) (Model, error) {
	if provider != p.Catalog.Provider || accountID != p.Catalog.AccountID {
		return Model{}, fmt.Errorf("picker catalog belongs to %s/%s", p.Catalog.Provider, p.Catalog.AccountID)
	}
	model, ok := p.Current()
	if !ok {
		return Model{}, fmt.Errorf("picker has no current model")
	}
	return model, nil
}

var (
	standardReasoningLevels = []string{"minimal", "low", "medium", "high", "xhigh"}
	grokReasoningLevels     = []string{"low", "medium", "high"}
	grokMultiAgentLevels    = []string{"low", "medium", "high", "xhigh"}
)

func AvailableReasoningLevels(provider string, model Model) []string {
	if len(model.ReasoningLevels) > 0 {
		return model.ReasoningLevels
	}
	if provider == "grok" {
		modelID := strings.ToLower(model.ID)
		if strings.Contains(modelID, "multi-agent") || strings.Contains(modelID, "4.20") {
			return grokMultiAgentLevels
		}
		if strings.Contains(modelID, "grok-4.5") || model.SupportsReasoning {
			return grokReasoningLevels
		}
		return nil
	}
	if !model.SupportsReasoning {
		return nil
	}
	return standardReasoningLevels
}

func PreferredReasoningLevel(provider string, model Model) string {
	levels := AvailableReasoningLevels(provider, model)
	if containsReasoningLevel(levels, model.DefaultReasoning) {
		return model.DefaultReasoning
	}
	if provider == "grok" && containsReasoningLevel(levels, "high") {
		return "high"
	}
	if len(levels) > 0 {
		return levels[0]
	}
	return ""
}

func ResolveReasoningEffort(provider string, model Model, requested string) (string, error) {
	levels := AvailableReasoningLevels(provider, model)
	if len(levels) == 0 {
		return "", nil
	}
	if requested == "" {
		return PreferredReasoningLevel(provider, model), nil
	}
	if containsReasoningLevel(levels, requested) {
		return requested, nil
	}
	return "", fmt.Errorf(
		"reasoning effort %q is not supported by %s/%s; choose %s",
		requested,
		provider,
		model.ID,
		strings.Join(levels, ", "),
	)
}

func containsReasoningLevel(levels []string, wanted string) bool {
	for _, level := range levels {
		if level == wanted {
			return true
		}
	}
	return false
}
