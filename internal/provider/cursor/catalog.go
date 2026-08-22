package cursor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/netproxy"
	"github.com/Viking602/azem/internal/provider/catalog"
)

const (
	DefaultAPIURL        = "https://api2.cursor.sh"
	DefaultClientVersion = "cli-2026.07.23-e383d2b"
	usableModelsPath     = "/agent.v1.AgentService/GetUsableModels"
	runPath              = "/agent.v1.AgentService/Run"
)

var bundledModels = []catalog.Model{
	{ID: "auto-smart", Name: "Auto", Aliases: []string{"auto", "cursor-router"}, SupportsTools: true, SupportsReasoning: true, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, ContextWindow: 200000, MaxOutputTokens: 64000},
	{ID: "composer-2", Name: "Composer 2", Aliases: []string{"composer", "composer-latest"}, SupportsTools: true, SupportsReasoning: true, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, ContextWindow: 200000, MaxOutputTokens: 64000},
	{ID: "composer-1.5", Name: "Composer 1.5", SupportsTools: true, InputModalities: []string{"text"}, OutputModalities: []string{"text"}, ContextWindow: 200000, MaxOutputTokens: 64000},
	{ID: "claude-4.6-opus-high", Name: "Claude 4.6 Opus", Aliases: []string{"claude-4.6-opus"}, SupportsTools: true, SupportsReasoning: true, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, ContextWindow: 200000, MaxOutputTokens: 64000, ReasoningLevels: []string{"low", "medium", "high"}},
	{ID: "claude-4.6-sonnet", Name: "Claude 4.6 Sonnet", SupportsTools: true, SupportsReasoning: true, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, ContextWindow: 200000, MaxOutputTokens: 64000, ReasoningLevels: []string{"low", "medium", "high"}},
	{ID: "gpt-5.4", Name: "GPT-5.4", SupportsTools: true, SupportsReasoning: true, InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}, ContextWindow: 272000, MaxOutputTokens: 64000, ReasoningLevels: []string{"low", "medium", "high"}},
}

type CatalogConfig struct {
	BaseURL       string
	AccessToken   string
	ClientVersion string
	Client        *http.Client
}

func FetchUsableModels(ctx context.Context, config CatalogConfig) ([]catalog.Model, error) {
	client := config.Client
	if client == nil {
		client = netproxy.NewHTTPClient(20 * time.Second)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultAPIURL
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+usableModelsPath, bytes.NewReader(nil))
	if err != nil {
		return nil, err
	}
	applyCursorHeaders(request.Header, config.AccessToken, config.ClientVersion)
	request.Header.Set("Content-Type", "application/proto")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode/100 != 2 {
		return nil, fmt.Errorf("cursor GetUsableModels returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	models, err := decodeUsableModels(payload)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("cursor GetUsableModels returned no usable models")
	}
	return models, nil
}

func BundledModels() []catalog.Model {
	return cloneBundledModels()
}

func cloneBundledModels() []catalog.Model {
	models := make([]catalog.Model, len(bundledModels))
	copy(models, bundledModels)
	for index, model := range models {
		models[index].Aliases = append([]string(nil), model.Aliases...)
		models[index].ReasoningLevels = append([]string(nil), model.ReasoningLevels...)
		models[index].InputModalities = append([]string(nil), model.InputModalities...)
		models[index].OutputModalities = append([]string(nil), model.OutputModalities...)
		models[index] = catalog.NormalizeCursorModel(models[index])
	}
	return models
}

func decodeUsableModels(payload []byte) ([]catalog.Model, error) {
	frames, err := decodeConnectFrames(payload)
	if err != nil || len(frames) == 0 {
		frames = [][]byte{payload}
	}
	var models []catalog.Model
	for _, frame := range frames {
		fields, err := decodeFields(frame)
		if err != nil {
			continue
		}
		for _, raw := range fieldRepeated(fields, 1) {
			if model, ok := decodeModelDetails(raw); ok {
				models = append(models, model)
			}
		}
	}
	return models, nil
}

func decodeModelDetails(payload []byte) (catalog.Model, bool) {
	fields, err := decodeFields(payload)
	if err != nil {
		return catalog.Model{}, false
	}
	id := strings.TrimSpace(fieldString(fields, fieldModelID))
	displayID := strings.TrimSpace(fieldString(fields, fieldModelDisplayID))
	if id == "" {
		id = displayID
	}
	if id == "" {
		return catalog.Model{}, false
	}
	displayName := strings.TrimSpace(fieldString(fields, fieldModelDisplayName))
	displayShort := strings.TrimSpace(fieldString(fields, fieldModelDisplayShort))
	name := firstNonEmpty(displayName, displayShort, displayID, id)
	aliases := make([]string, 0, 4)
	if displayID != "" && displayID != id {
		aliases = append(aliases, displayID)
	}
	for _, alias := range fieldRepeated(fields, fieldModelAlias) {
		if text := strings.TrimSpace(string(alias)); text != "" && text != id && !containsString(aliases, text) {
			aliases = append(aliases, text)
		}
	}
	identity := strings.ToLower(id)
	model := catalog.Model{
		ID: id, Name: name, Aliases: aliases, SupportsTools: true,
		SupportsReasoning: hasProtoField(fields, fieldModelThinking, 2) || cursorFamilyReasons(identity),
		CursorMaxMode:     fieldUint32(fields, fieldModelMaxMode) != 0,
		ContextWindow:     200_000, MaxOutputTokens: 64_000,
	}
	if cursorOneMillionSignal(id, displayID, displayName, displayShort, aliases) {
		model.ContextWindow = 1_000_000
	}
	return catalog.NormalizeCursorModel(model), true
}

func hasProtoField(fields []protoField, number, wire int) bool {
	for _, field := range fields {
		if field.Field == number && field.Wire == wire {
			return true
		}
	}
	return false
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func cursorFamilyReasons(id string) bool {
	return strings.Contains(id, "claude") ||
		strings.Contains(id, "gemini") ||
		strings.Contains(id, "kimi-k3") ||
		strings.Contains(id, "glm-5.2") ||
		strings.Contains(id, "glm-5.3") ||
		strings.Contains(id, "glm-6")
}

func cursorOneMillionSignal(id, displayID, displayName, displayShort string, aliases []string) bool {
	for _, value := range []string{id, displayID, displayName, displayShort} {
		if hasCursorOneMillionLabel(value) {
			return true
		}
	}
	for _, alias := range aliases {
		if hasCursorOneMillionLabel(alias) {
			return true
		}
	}
	return false
}

func hasCursorOneMillionLabel(value string) bool {
	for _, part := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	}) {
		if part == "1m" {
			return true
		}
	}
	return false
}

func applyCursorHeaders(header http.Header, accessToken, clientVersion string) {
	if strings.TrimSpace(clientVersion) == "" {
		clientVersion = DefaultClientVersion
	}
	header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	header.Set("X-Ghost-Mode", "true")
	header.Set("X-Cursor-Client-Version", clientVersion)
	header.Set("X-Cursor-Client-Type", "cli")
}

func blobID(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
