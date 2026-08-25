package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/venat/message"
	"github.com/Viking602/venat/tool"
)

func TestImageGenerationValidatesInputsAndReturnsImageParts(t *testing.T) {
	root := t.TempDir()
	inputPath := filepath.Join(root, "input.png")
	writeTestFile(t, inputPath, string([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}))
	var captured map[string]any
	driver := &imageGenDriver{root: root, bridge: newLSPBridgeRuntime(), networkPolicy: "allow"}
	driver.execute = func(_ context.Context, _, _ string, params map[string]any) (lspBridgeResponse, error) {
		captured = params
		parts, _ := json.Marshal([]map[string]any{{"type": "text", "text": "generated"}, {"type": "image", "data": base64.StdEncoding.EncodeToString([]byte("image-bytes")), "mimeType": "image/png"}})
		return lspBridgeResponse{OK: true, Content: "generated", Parts: parts, Details: json.RawMessage(`{"provider":"fixture","imageCount":1}`)}, nil
	}
	params := map[string]any{"subject": "A precise test image", "aspect_ratio": "1:1", "provider": "gemini", "input": []any{map[string]any{"path": "input.png"}}}
	arguments, _ := json.Marshal(params)
	result, err := driver.Execute(context.Background(), tool.Call{ID: "image", Name: ToolGenerateImage, Arguments: arguments}, nil)
	if err != nil || result.IsError || len(result.Parts) != 1 || result.Parts[0].Kind != message.ContentImage || string(result.Parts[0].Data) != "image-bytes" {
		t.Fatalf("image generation = %#v, %v", result, err)
	}
	inputs := captured["input"].([]any)
	resolvedInput, err := filepath.EvalSymlinks(inputPath)
	if path := inputs[0].(map[string]any)["path"].(string); err != nil || path != resolvedInput {
		t.Fatalf("image input path = %q, resolved=%q, err=%v", path, resolvedInput, err)
	}
}

func TestTTSValidatesWorkspaceOutputAndForwardsRequest(t *testing.T) {
	root := t.TempDir()
	var captured map[string]any
	driver := &ttsDriver{root: root, bridge: newLSPBridgeRuntime()}
	driver.execute = func(_ context.Context, _, _ string, params map[string]any) (lspBridgeResponse, error) {
		captured = params
		return lspBridgeResponse{OK: true, Content: "Saved speech.wav", Details: json.RawMessage(`{"bytes":44,"codec":"wav","backend":"local"}`)}, nil
	}
	arguments := json.RawMessage(`{"text":"hello","voice_id":"eve","language":"en","output_path":"speech.wav","sample_rate":24000}`)
	result, err := driver.Execute(context.Background(), tool.Call{ID: "tts", Name: ToolTTS, Arguments: arguments}, nil)
	if err != nil || result.IsError || !strings.Contains(result.Content, "speech.wav") {
		t.Fatalf("TTS = %#v, %v", result, err)
	}
	if captured["output_path"] != filepath.Join(root, "speech.wav") {
		t.Fatalf("TTS output path = %#v", captured["output_path"])
	}
	outside := filepath.Join(t.TempDir(), "outside.wav")
	if err := os.Symlink(outside, filepath.Join(root, "linked.wav")); err != nil {
		t.Fatal(err)
	}
	linked := json.RawMessage(`{"text":"hello","output_path":"linked.wav"}`)
	result, err = driver.Execute(context.Background(), tool.Call{ID: "linked", Name: ToolTTS, Arguments: linked}, nil)
	if err != nil || !result.IsError || !strings.Contains(result.Content, "symlink") {
		t.Fatalf("TTS symlink = %#v, %v", result, err)
	}
}

func TestImageGenerationHonorsNetworkPolicy(t *testing.T) {
	driver := newImageGenDriver(t.TempDir(), newLSPBridgeRuntime(), "deny")
	result, err := driver.Execute(context.Background(), tool.Call{ID: "image", Name: ToolGenerateImage, Arguments: json.RawMessage(`{"subject":"test"}`)}, nil)
	if err != nil || !result.IsError || !strings.Contains(result.Content, "network policy") {
		t.Fatalf("image network denial = %#v, %v", result, err)
	}
}

func TestTTSLiveLocal(t *testing.T) {
	if os.Getenv("AZEM_LIVE_TTS") != "1" {
		t.Skip("set AZEM_LIVE_TTS=1 for local model synthesis")
	}
	root := t.TempDir()
	bridge := newLSPBridgeRuntime()
	t.Cleanup(func() { _ = bridge.Close(context.Background()) })
	driver := newTTSDriver(root, bridge)
	result, err := driver.Execute(context.Background(), tool.Call{ID: "tts", Name: ToolTTS, Arguments: json.RawMessage(`{"text":"hello","output_path":"speech.wav"}`)}, nil)
	if err != nil || result.IsError {
		t.Fatalf("live TTS = %#v, %v", result, err)
	}
	if info, err := os.Stat(filepath.Join(root, "speech.wav")); err != nil || info.Size() <= 44 {
		t.Fatalf("live TTS output = %v, %v", info, err)
	}
}
