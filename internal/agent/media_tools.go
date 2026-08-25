package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/tool"
)

const (
	ToolGenerateImage = "generate_image"
	ToolTTS           = "tts"
)

type mediaExecutor func(context.Context, string, string, map[string]any) (lspBridgeResponse, error)

type imageGenDriver struct {
	root          string
	bridge        *lspBridgeRuntime
	networkPolicy string
	execute       mediaExecutor
}

type ttsDriver struct {
	root    string
	bridge  *lspBridgeRuntime
	execute mediaExecutor
}

type mediaToolResult struct {
	Tool    string          `json:"tool"`
	Content string          `json:"content"`
	Details json.RawMessage `json:"details,omitempty"`
}

func newImageGenDriver(root string, bridge *lspBridgeRuntime, networkPolicy string) tool.Driver {
	if bridge == nil {
		bridge = newLSPBridgeRuntime()
	}
	driver := &imageGenDriver{root: root, bridge: bridge, networkPolicy: networkPolicy}
	driver.execute = func(ctx context.Context, cwd, sessionID string, params map[string]any) (lspBridgeResponse, error) {
		return bridge.requestMedia(ctx, ToolGenerateImage, cwd, sessionID, params)
	}
	return driver
}

func newTTSDriver(root string, bridge *lspBridgeRuntime) tool.Driver {
	if bridge == nil {
		bridge = newLSPBridgeRuntime()
	}
	driver := &ttsDriver{root: root, bridge: bridge}
	driver.execute = func(ctx context.Context, cwd, sessionID string, params map[string]any) (lspBridgeResponse, error) {
		return bridge.requestMedia(ctx, ToolTTS, cwd, sessionID, params)
	}
	return driver
}

func (driver *imageGenDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        ToolGenerateImage,
		Description: "Generate or edit images through the configured image-provider fallback chain. Give one detailed subject; optional action, scene, composition, lighting, style, short rendered text, changes, aspect ratio, image size, input images, and per-request provider override.",
		InputSchema: tool.Schema{Type: "object", Required: []string{"subject"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{
			"subject": {Type: "string"}, "action": {Type: "string"}, "scene": {Type: "string"}, "composition": {Type: "string"},
			"lighting": {Type: "string"}, "style": {Type: "string"}, "text": {Type: "string"}, "changes": {Type: "array", Items: &tool.Schema{Type: "string"}},
			"aspect_ratio": {Type: "string"}, "image_size": {Type: "string"}, "input": {Type: "array", Items: &tool.Schema{Type: "object"}}, "provider": {Type: "string"},
		}}, EffectType: tool.EffectWrite, RequiresApproval: true, RequiresActionTask: true, RiskLevel: "medium",
		PolicyTags: []string{"media", "image", "network", "write"}, Concurrency: tool.ConcurrencyParallel,
	}
}

func (driver *ttsDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        ToolTTS,
		Description: "Generate a speech audio file from text. Auto chooses on-device Kokoro WAV or xAI Grok Voice when configured; voice_id/language/sample_rate/bit_rate are request options. Local synthesis always writes WAV.",
		InputSchema: tool.Schema{Type: "object", Required: []string{"text", "output_path"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{
			"text": {Type: "string"}, "voice_id": {Type: "string"}, "language": {Type: "string"}, "output_path": {Type: "string"},
			"sample_rate": {Type: "integer"}, "bit_rate": {Type: "integer"},
		}}, EffectType: tool.EffectWrite, RequiresApproval: true, RequiresActionTask: true, RiskLevel: "medium",
		PolicyTags: []string{"media", "speech", "write"}, Concurrency: tool.ConcurrencyExclusive, ConcurrencyGroup: "speech-generation",
	}
}

func (driver *imageGenDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var params map[string]any
	if err := json.Unmarshal(call.Arguments, &params); err != nil {
		return mediaError(call, fmt.Errorf("decode arguments: %w", err)), nil
	}
	if driver.networkPolicy == "deny" {
		return mediaError(call, errors.New("image generation is denied by workspace network policy")), nil
	}
	if err := validateImageGenParams(driver.root, params); err != nil {
		return mediaError(call, err), nil
	}
	return executeMedia(ctx, call, driver.root, ToolGenerateImage, params, driver.execute)
}

func (driver *ttsDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var params map[string]any
	if err := json.Unmarshal(call.Arguments, &params); err != nil {
		return mediaError(call, fmt.Errorf("decode arguments: %w", err)), nil
	}
	if err := validateTTSParams(driver.root, params); err != nil {
		return mediaError(call, err), nil
	}
	return executeMedia(ctx, call, driver.root, ToolTTS, params, driver.execute)
}

func executeMedia(ctx context.Context, call tool.Call, root, toolName string, params map[string]any, execute mediaExecutor) (tool.Result, error) {
	caller, _ := tool.CallerFromContext(ctx)
	sessionID := firstString(caller.SessionID, caller.TeamRunID, root)
	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	response, err := execute(requestCtx, root, sessionID, params)
	if err != nil {
		return mediaError(call, err), nil
	}
	isError := response.IsError
	if len(response.Details) > 0 {
		var details struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(response.Details, &details) == nil && details.Error != "" {
			isError = true
		}
	}
	result := mediaToolResult{Tool: toolName, Content: response.Content, Details: response.Details}
	structured, _ := json.Marshal(result)
	toolResult := tool.Result{ToolCallID: call.ID, Name: call.Name, Content: response.Content, Structured: structured, IsError: isError}
	toolResult.Parts = decodeMediaParts(response.Parts)
	return toolResult, nil
}

func validateImageGenParams(root string, params map[string]any) error {
	subject, _ := params["subject"].(string)
	if strings.TrimSpace(subject) == "" || len(subject) > 20000 {
		return errors.New("subject is required and must not exceed 20000 characters")
	}
	if ratio, ok := params["aspect_ratio"].(string); ok && ratio != "" {
		valid := map[string]bool{"1:1": true, "3:4": true, "4:3": true, "9:16": true, "16:9": true, "3:2": true, "2:3": true}
		if !valid[ratio] {
			return errors.New("aspect_ratio is unsupported")
		}
	}
	if size, ok := params["image_size"].(string); ok && size != "" && size != "1024x1024" && size != "1536x1024" && size != "1024x1536" {
		return errors.New("image_size is unsupported")
	}
	if provider, ok := params["provider"].(string); ok && provider != "" {
		valid := map[string]bool{"auto": true, "openai": true, "openai-codex": true, "antigravity": true, "xai": true, "openrouter": true, "gemini": true}
		if !valid[provider] {
			return errors.New("image provider is unsupported")
		}
	}
	if inputs, ok := params["input"].([]any); ok {
		if len(inputs) > 16 {
			return errors.New("input has more than 16 images")
		}
		for _, raw := range inputs {
			input, ok := raw.(map[string]any)
			if !ok {
				return errors.New("input image is invalid")
			}
			path, _ := input["path"].(string)
			data, _ := input["data"].(string)
			if path == "" && data == "" || path != "" && data != "" {
				return errors.New("each input image requires exactly one of path or data")
			}
			if path != "" {
				absolute, _, info, err := secureReadPath(root, path)
				if err != nil || !info.Mode().IsRegular() || info.Size() > 35<<20 {
					return errors.New("input image path is invalid or exceeds 35 MiB")
				}
				input["path"] = absolute
			} else if decoded, err := decodeImageInputData(data); err != nil || len(decoded) > 35<<20 {
				return errors.New("input image data is invalid or exceeds 35 MiB")
			}
		}
	}
	return nil
}

func validateTTSParams(root string, params map[string]any) error {
	text, _ := params["text"].(string)
	if len(text) < 1 || len(text) > 15000 {
		return errors.New("text must contain 1-15000 characters")
	}
	output, _ := params["output_path"].(string)
	if strings.TrimSpace(output) == "" {
		return errors.New("output_path is required")
	}
	relative, err := workspaceRelativePath(root, output)
	if err != nil {
		return err
	}
	workspace, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer workspace.Close()
	parent := filepath.Dir(filepath.FromSlash(relative))
	if info, err := workspace.Stat(parent); err != nil || !info.IsDir() {
		return errors.New("output_path parent directory does not exist")
	}
	if info, err := workspace.Lstat(filepath.FromSlash(relative)); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("output_path must not be a symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	params["output_path"] = filepath.Join(root, filepath.FromSlash(relative))
	for _, key := range []string{"sample_rate", "bit_rate"} {
		if value, ok := params[key].(float64); ok && (value <= 0 || value != float64(int(value)) || value > 1_000_000) {
			return fmt.Errorf("%s is outside the supported range", key)
		}
	}
	return nil
}

func decodeImageInputData(data string) ([]byte, error) {
	if strings.HasPrefix(data, "data:") {
		_, encoded, found := strings.Cut(data, ",")
		if !found {
			return nil, errors.New("invalid data URL")
		}
		data = encoded
	}
	return base64.StdEncoding.DecodeString(data)
}

func decodeMediaParts(payload json.RawMessage) []message.ContentPart {
	if len(payload) == 0 {
		return nil
	}
	var parts []evalBridgePart
	if json.Unmarshal(payload, &parts) != nil {
		return nil
	}
	result := make([]message.ContentPart, 0, len(parts))
	for _, part := range parts {
		if part.Type != "image" || part.Data == "" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(part.Data)
		if err == nil {
			result = append(result, message.ContentPart{Kind: message.ContentImage, Data: data, MediaType: part.MimeType})
		}
	}
	return result
}

func mediaError(call tool.Call, err error) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: call.Name + " failed: " + err.Error(), IsError: true}
}
