package catalog

import (
	"fmt"
	"sort"
	"strconv"
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
	grokOAuthCuratedModels  = []Model{
		{ID: "grok-build", Name: "Grok Build", ContextWindow: 512_000, MaxOutputTokens: 512_000, SupportsTools: true, SupportsReasoning: true, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}},
		{ID: "grok-build-0.1", Name: "Grok Build 0.1", ContextWindow: 256_000, MaxOutputTokens: 256_000, SupportsTools: true, SupportsReasoning: true, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}},
		{ID: "grok-4.3", Name: "Grok 4.3", ContextWindow: 1_000_000, MaxOutputTokens: 1_000_000, ReasoningLevels: []string{"low", "medium", "high"}, DefaultReasoning: "high", SupportsTools: true, SupportsReasoning: true, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}},
		{ID: "grok-4.5", Name: "Grok 4.5", ContextWindow: 500_000, MaxOutputTokens: 500_000, ReasoningLevels: []string{"low", "medium", "high"}, DefaultReasoning: "high", SupportsTools: true, SupportsReasoning: true, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}},
		{ID: "grok-4.6", Name: "Grok 4.6", ContextWindow: 500_000, MaxOutputTokens: 500_000, ReasoningLevels: []string{"low", "medium", "high", "xhigh"}, DefaultReasoning: "high", SupportsTools: true, SupportsReasoning: true, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}},
		{ID: "grok-4.20-multi-agent-0309", Name: "Grok 4.20 (Multi-Agent)", ContextWindow: 2_000_000, MaxOutputTokens: 2_000_000, ReasoningLevels: []string{"low", "medium", "high", "xhigh"}, DefaultReasoning: "high", SupportsTools: true, SupportsReasoning: true, InputModalities: []string{"text"}, OutputModalities: []string{"text"}},
		{ID: "grok-4.20-0309-reasoning", Name: "Grok 4.20 (Reasoning)", ContextWindow: 2_000_000, MaxOutputTokens: 2_000_000, SupportsTools: true, SupportsReasoning: true, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}},
		{ID: "grok-4.20-0309-non-reasoning", Name: "Grok 4.20 (Non-Reasoning)", ContextWindow: 2_000_000, MaxOutputTokens: 2_000_000, SupportsTools: true, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}},
		{ID: "grok-composer-2.5-fast", Name: "Grok Composer 2.5 Fast", ContextWindow: 200_000, MaxOutputTokens: 200_000, SupportsTools: true, InputModalities: []string{"text"}, OutputModalities: []string{"text"}},
	}
)

func AvailableReasoningLevels(provider string, model Model) []string {
	switch provider {
	case "grok":
		if levels := grokReasoningLevelsForID(model.ID); len(levels) > 0 {
			return levels
		}
		if grokOmitsReasoningEffort(model.ID) {
			return nil
		}
		return model.ReasoningLevels
	case "cursor":
		if len(model.ReasoningLevels) > 0 {
			return model.ReasoningLevels
		}
		return []string{cursorModelTier(model.ID)}
	}
	if len(model.ReasoningLevels) > 0 {
		return model.ReasoningLevels
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

func normalizeProviderModels(provider string, models []Model) []Model {
	switch provider {
	case "grok":
		return normalizeGrokModels(models)
	case "cursor":
		for index := range models {
			models[index] = NormalizeCursorModel(models[index])
		}
	}
	return models
}

func normalizeGrokModels(models []Model) []Model {
	if len(models) == 0 {
		return models
	}
	filtered := models[:0]
	indexByID := make(map[string]int, len(models)+len(grokOAuthCuratedModels))
	for _, model := range models {
		id := strings.ToLower(strings.TrimSpace(model.ID))
		if strings.HasPrefix(id, "grok-imagine-") || strings.HasPrefix(id, "grok-stt-") || strings.HasPrefix(id, "grok-voice-") {
			continue
		}
		model = normalizeGrokModel(model)
		indexByID[id] = len(filtered)
		filtered = append(filtered, model)
	}
	if len(filtered) == 0 {
		return filtered
	}
	for _, template := range grokOAuthCuratedModels {
		id := strings.ToLower(template.ID)
		if index, ok := indexByID[id]; ok {
			filtered[index] = overlayCuratedModel(filtered[index], template)
			continue
		}
		indexByID[id] = len(filtered)
		filtered = append(filtered, cloneModel(template))
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].ID < filtered[j].ID })
	return filtered
}

func normalizeGrokModel(model Model) Model {
	levels := grokReasoningLevelsForID(model.ID)
	if len(levels) == 0 && !grokOmitsReasoningEffort(model.ID) {
		levels = model.ReasoningLevels
	}
	model.ReasoningLevels = append(model.ReasoningLevels[:0], levels...)
	model.DefaultReasoning = ""
	if len(levels) > 0 {
		model.SupportsReasoning = true
		model.DefaultReasoning = "high"
	}
	id := strings.ToLower(model.ID)
	if strings.Contains(id, "non-reasoning") {
		model.SupportsReasoning = false
	} else if strings.Contains(id, "reasoning") || strings.HasPrefix(id, "grok-build") {
		model.SupportsReasoning = true
	}
	return model
}

func grokReasoningLevelsForID(modelID string) []string {
	id := strings.ToLower(strings.TrimSpace(modelID))
	switch {
	case id == "grok-4.20", strings.HasPrefix(id, "grok-4.20-multi-agent"), strings.HasPrefix(id, "grok-4.6"):
		return grokMultiAgentLevels
	case strings.HasPrefix(id, "grok-3-mini"), strings.HasPrefix(id, "grok-4.3"), strings.HasPrefix(id, "grok-4.5"):
		return grokReasoningLevels
	default:
		return nil
	}
}

func grokOmitsReasoningEffort(modelID string) bool {
	id := strings.ToLower(strings.TrimSpace(modelID))
	return strings.HasPrefix(id, "grok-build") ||
		strings.HasPrefix(id, "grok-composer-") ||
		strings.Contains(id, "grok-4.20-0309-reasoning") ||
		strings.Contains(id, "grok-4.20-0309-non-reasoning")
}

func overlayCuratedModel(base, curated Model) Model {
	base.Name = curated.Name
	base.ContextWindow = curated.ContextWindow
	base.MaxOutputTokens = curated.MaxOutputTokens
	base.ReasoningLevels = append(base.ReasoningLevels[:0], curated.ReasoningLevels...)
	base.DefaultReasoning = curated.DefaultReasoning
	base.SupportsTools = curated.SupportsTools
	base.SupportsReasoning = curated.SupportsReasoning
	base.InputModalities = append(base.InputModalities[:0], curated.InputModalities...)
	base.OutputModalities = append(base.OutputModalities[:0], curated.OutputModalities...)
	return base
}

func cloneModel(model Model) Model {
	model.Aliases = append([]string(nil), model.Aliases...)
	model.ReasoningLevels = append([]string(nil), model.ReasoningLevels...)
	model.InputModalities = append([]string(nil), model.InputModalities...)
	model.OutputModalities = append([]string(nil), model.OutputModalities...)
	model.ServiceTiers = append([]ServiceTier(nil), model.ServiceTiers...)
	model.AdditionalSpeedTiers = append([]string(nil), model.AdditionalSpeedTiers...)
	return model
}

// NormalizeCursorModel derives the stable Cursor tier and effective context
// from GetUsableModels fields. The endpoint has no context-window field.
func NormalizeCursorModel(model Model) Model {
	tier := cursorModelTier(model.ID)
	model.ReasoningLevels = []string{tier}
	model.DefaultReasoning = tier
	if model.ContextWindow <= 0 {
		model.ContextWindow = 200_000
	}
	if cursorModelHasMillionContext(model) && model.ContextWindow < 1_000_000 {
		model.ContextWindow = 1_000_000
	}
	if model.MaxOutputTokens <= 0 {
		model.MaxOutputTokens = 64_000
	}
	return model
}

func EnsureCursorModalities(model Model) Model {
	if len(model.OutputModalities) == 0 {
		model.OutputModalities = []string{"text"}
	}
	if len(model.InputModalities) > 0 {
		return model
	}
	identity := strings.ToLower(model.ID + " " + model.Name)
	if strings.Contains(identity, "claude") || strings.Contains(identity, "gemini") || strings.Contains(identity, "gpt-") || strings.Contains(identity, "codex") {
		model.InputModalities = []string{"text", "image"}
		return model
	}
	model.InputModalities = []string{"text"}
	return model
}

func cursorModelTier(modelID string) string {
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(modelID)), func(r rune) bool { return r == '-' })
	if len(parts) == 0 {
		return "default"
	}
	if parts[len(parts)-1] == "fast" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 0 && parts[len(parts)-1] == "thinking" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) >= 2 && parts[len(parts)-2] == "extra" && parts[len(parts)-1] == "high" {
		return "xhigh"
	}
	if len(parts) == 0 {
		return "default"
	}
	switch tier := parts[len(parts)-1]; tier {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return tier
	default:
		return "default"
	}
}

func cursorModelHasMillionContext(model Model) bool {
	if hasOneMillionLabel(model.ID) || hasOneMillionLabel(model.Name) {
		return true
	}
	for _, alias := range model.Aliases {
		if hasOneMillionLabel(alias) {
			return true
		}
	}
	id := strings.ToLower(strings.TrimSpace(model.ID))
	bare := id
	if slash := strings.LastIndexByte(bare, '/'); slash >= 0 {
		bare = bare[slash+1:]
	}
	if (bare == "k3" || strings.HasPrefix(bare, "kimi-k3")) && !strings.Contains(bare, "256k") {
		return true
	}
	if cursorGLMHasMillionContext(bare) {
		return true
	}
	return model.CursorMaxMode && (strings.Contains(id, "claude") || strings.Contains(id, "gemini"))
}

func cursorGLMHasMillionContext(modelID string) bool {
	if !strings.HasPrefix(modelID, "glm-") || strings.Contains(modelID, "vision") {
		return false
	}
	version := strings.TrimPrefix(modelID, "glm-")
	if dash := strings.IndexByte(version, '-'); dash >= 0 {
		version = version[:dash]
	}
	parts := strings.SplitN(version, ".", 3)
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	minor := 0
	if len(parts) > 1 {
		minor, _ = strconv.Atoi(parts[1])
	}
	return major > 5 || major == 5 && minor >= 2
}

func hasOneMillionLabel(value string) bool {
	value = strings.ToLower(value)
	for index := 0; index+2 <= len(value); index++ {
		if value[index:index+2] != "1m" {
			continue
		}
		leftBoundary := index == 0 || !isASCIIAlphaNumeric(value[index-1])
		rightBoundary := index+2 == len(value) || !isASCIIAlphaNumeric(value[index+2])
		if leftBoundary && rightBoundary {
			return true
		}
	}
	return false
}

func isASCIIAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}
