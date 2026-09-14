package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	hyagent "github.com/Viking602/venat/agent"

	"github.com/Viking602/azem/internal/auth"
	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

func TestInspectImageReachesResponsesProvider(t *testing.T) {
	harness := newSkillRuntimeHarness(t, "---\nname: demo\ndescription: stable catalog\n---\nstable body\n", nil, func(call int, body string, writer http.ResponseWriter) {
		switch call {
		case 1:
			writeProviderToolCall(writer, "inspect-image", "image-1", "inspect_image", `{"path":"plot.png"}`)
		case 2:
			var request struct {
				Input []struct {
					Type   string          `json:"type"`
					CallID string          `json:"call_id"`
					Output json.RawMessage `json:"output"`
				} `json:"input"`
			}
			if err := json.Unmarshal([]byte(body), &request); err != nil {
				t.Error(err)
			}
			found := false
			for _, item := range request.Input {
				if item.Type == "function_call_output" && item.CallID == "image-1" {
					found = strings.Contains(string(item.Output), `"type":"input_image"`) &&
						strings.Contains(string(item.Output), "data:image/png;base64,"+base64.StdEncoding.EncodeToString(minimalPNG()))
				}
			}
			if !found {
				t.Error("native inspect_image lost image bytes before the provider HTTP request")
			}
			writeProviderText(writer, "image-finish", "Inspected the image.")
		default:
			t.Errorf("unexpected provider call %d", call)
			writeProviderText(writer, "image-extra", "Unexpected call.")
		}
	})
	if err := os.WriteFile(filepath.Join(harness.workspace, "plot.png"), minimalPNG(), 0o600); err != nil {
		t.Fatal(err)
	}
	runID, err := harness.service.StartConfiguredTurn(TurnRequest{
		SessionID: "image-e2e", Prompt: "Inspect plot.png and describe it.",
		Provider: "chatgpt", Model: "gpt-skill", Reasoning: "minimal", AgentMode: "single",
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForProviderRun(t, harness.service, runID)
	if got := harness.calls.Load(); got != 2 {
		t.Fatalf("provider calls=%d, want inspect then finish", got)
	}
}

func TestModelImageInputSupportUsesConfiguredLLMuxModalities(t *testing.T) {
	cfg := config.Default()
	cfg.Providers.LLMux["deepseek"] = config.LLMuxProviderConfig{
		Enabled: true,
		Models: []config.LLMuxModelConfig{
			{ID: "text-only", InputModalities: []string{"text"}},
			{ID: "vision", InputModalities: []string{"text", "image"}},
			{ID: "unknown"},
		},
	}
	runtime := &ProviderRuntime{cfg: cfg}
	tests := []struct {
		model     string
		known     bool
		supported bool
	}{
		{model: "text-only", known: true, supported: false},
		{model: "vision", known: true, supported: true},
		{model: "unknown", known: false, supported: false},
	}
	for _, test := range tests {
		known, supported, err := runtime.modelImageInputSupport(context.Background(), "deepseek", "", test.model)
		if err != nil || known != test.known || supported != test.supported {
			t.Fatalf("model %s support=(%v,%v), error=%v; want (%v,%v)", test.model, known, supported, err, test.known, test.supported)
		}
	}
}

func TestTurnContextUsesPrivateVisionEvidenceInsteadOfDirectImages(t *testing.T) {
	request := TurnRequest{
		Images:        []session.Attachment{{ID: "image-1", Name: "screen.png", MIME: "image/png", Path: "screen.png"}},
		visionContext: "Button text: Deploy. A red box surrounds the error.",
	}
	manager := turnContext{
		instructions: "rules", providerID: "deepseek", modelID: "text-only",
		visionContext: request.visionContext,
		images:        effectiveTurnImages(request),
	}
	messages, err := manager.Build(context.Background(), hyagent.Request{Prompt: "fix the screenshot issue"})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatalf("messages=%#v", messages)
	}
	if !isPrivateMessage(messages[1]) || !strings.Contains(messages[1].Text, visionEvidenceLabel) || !strings.Contains(messages[1].Text, "Button text: Deploy") {
		t.Fatalf("vision evidence message=%#v", messages[1])
	}
	if attachments := AttachmentsFromMessage(messages[2]); len(attachments) != 0 {
		t.Fatalf("direct images leaked to the text-only main model: %#v", attachments)
	}
	if images := effectiveTurnImages(request); len(images) != 0 {
		t.Fatalf("effective images=%#v, want none after vision assistance", images)
	}
}

func TestTeamPromptIncludesUntrustedVisionEvidence(t *testing.T) {
	got := teamPrompt(TurnRequest{Prompt: "inspect", visionContext: "chart rises from 10 to 20"})
	if !strings.HasPrefix(got, "inspect\n\n"+visionEvidenceLabel) || !strings.Contains(got, "<visual-evidence>") {
		t.Fatalf("team prompt=%q", got)
	}
}

func TestPrepareVisionAssistanceCallsConfiguredImageModel(t *testing.T) {
	ctx := context.Background()
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/messages" || request.Header.Get("X-Api-Key") != "vision-key" {
			t.Errorf("path/key = %q/%q", request.URL.Path, request.Header.Get("X-Api-Key"))
		}
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Error(err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(response, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-1\",\"model\":\"vision-model\",\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n")
		_, _ = fmt.Fprint(response, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		_, _ = fmt.Fprint(response, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Red error banner: build failed\"}}\n\n")
		_, _ = fmt.Fprint(response, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		_, _ = fmt.Fprint(response, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":6}}\n\n")
		_, _ = fmt.Fprint(response, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	store, err := sqlitestore.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	credentials, err := auth.NewFileStore(filepath.Join(t.TempDir(), "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	authentication := auth.NewService(store.DB(), credentials, nil, nil)
	catalogService := catalog.NewService(store.DB(), authentication)
	cfg := config.Default()
	cfg.Providers.LLMux["deepseek"] = config.LLMuxProviderConfig{
		Enabled: true, BaseURL: server.URL,
		Models: []config.LLMuxModelConfig{
			{ID: "text-model", ContextWindow: 128000, MaxOutputTokens: 8192, InputModalities: []string{"text"}},
			{ID: "vision-model", ContextWindow: 128000, MaxOutputTokens: 8192, InputModalities: []string{"text", "image"}},
		},
	}
	cfg.Agents.Vision = config.ModelRouteConfig{Provider: "deepseek", Model: "vision-model", Reasoning: "low"}
	t.Setenv("DEEPSEEK_API_KEY", "vision-key")
	runtime := &ProviderRuntime{cfg: cfg, auth: authentication, catalog: catalogService}
	host := NewService(ctx, cfg)
	host.AttachAttachments(filepath.Join(t.TempDir(), "attachments"))
	runtime.host = host
	image, err := host.attachments.ImportBytes("session-1", "screen.png", "image/png", minimalPNG())
	if err != nil {
		t.Fatal(err)
	}

	prepared, err := runtime.prepareVisionAssistance(ctx, TurnRequest{
		SessionID: "session-1", Prompt: "fix this", Provider: "deepseek", Model: "text-model", Images: []session.Attachment{image},
	}, "run-1", "env:DEEPSEEK_API_KEY", "text-model")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.visionContext != "Red error banner: build failed" || len(effectiveTurnImages(prepared)) != 0 {
		t.Fatalf("prepared vision context=%q images=%#v", prepared.visionContext, effectiveTurnImages(prepared))
	}
	if received["model"] != "vision-model" || received["stream"] != true || !strings.Contains(fmt.Sprint(received["messages"]), "image") {
		t.Fatalf("vision wire request=%#v", received)
	}
}
