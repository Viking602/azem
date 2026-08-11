package llmuxdriver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/provider/responses"
	sdk "github.com/Viking602/llmux"
	"github.com/Viking602/llmux/provider/anthropic"
	"github.com/Viking602/llmux/provider/openai/compat"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
)

type sliceStream struct {
	parts []sdk.Part
}

func (s *sliceStream) Recv() (sdk.Part, error) {
	if len(s.parts) == 0 {
		return sdk.Part{}, io.EOF
	}
	part := s.parts[0]
	s.parts = s.parts[1:]
	return part, nil
}

func (*sliceStream) Close() error { return nil }

func TestProfilesAndStreamMapping(t *testing.T) {
	profiles := Profiles()
	foundOpenAI, foundOpenRouter := false, false
	for index, profile := range profiles {
		if strings.Contains(profile.ID, "_") {
			t.Fatalf("profile %q is not a canonical models.dev-style ID", profile.ID)
		}
		if index > 0 && profiles[index-1].ID >= profile.ID {
			t.Fatalf("profiles are not sorted and unique at %q", profile.ID)
		}
		if profile.ID == "chatgpt" || profile.ID == "grok" {
			t.Fatalf("reserved Azem provider leaked into llmux settings: %q", profile.ID)
		}
		foundOpenAI = foundOpenAI || profile.ID == "openai"
		foundOpenRouter = foundOpenRouter || profile.ID == "openrouter"
	}
	if !foundOpenAI || !foundOpenRouter {
		t.Fatalf("missing expected profiles: openai=%v openrouter=%v", foundOpenAI, foundOpenRouter)
	}
	if profile, ok := LookupProfile("opencode"); !ok || profile.ID != "opencode-zen" || profile.BaseURL != "https://opencode.ai/zen/v1" {
		t.Fatalf("opencode profile = %+v, found=%v", profile, ok)
	}
	stream := &streamAdapter{inner: &sliceStream{parts: []sdk.Part{
		{Kind: sdk.PartTextDelta, Delta: "hello"},
		{Kind: sdk.PartFinish, FinishReason: sdk.FinishStop, Usage: sdk.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}},
	}}}
	text, err := stream.Recv()
	if err != nil || text.Kind != hyprovider.EventTextDelta || text.Text != "hello" || text.TextPhase != "" {
		t.Fatalf("text event = %+v, error = %v", text, err)
	}
	done, err := stream.Recv()
	if err != nil || done.Kind != hyprovider.EventDone || done.StopReason != hyprovider.StopReasonComplete || done.Usage.TotalTokens != 5 {
		t.Fatalf("done event = %+v, error = %v", done, err)
	}
}

func TestConvertRequestHonorsMaxOutputTokens(t *testing.T) {
	fromExtra, _, err := convertRequest(hyprovider.Request{
		Model:     "deepseek-v4-pro",
		ExtraBody: map[string]any{"max_output_tokens": 384000},
		MaxTokens: 1024,
	}, "", "deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if fromExtra.Options.MaxOutputTokens == nil || *fromExtra.Options.MaxOutputTokens != 384000 {
		t.Fatalf("extra body max_output_tokens = %v, want 384000", fromExtra.Options.MaxOutputTokens)
	}

	fromMaxTokens, _, err := convertRequest(hyprovider.Request{
		Model:     "deepseek-v4-pro",
		MaxTokens: 64000,
	}, "", "deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if fromMaxTokens.Options.MaxOutputTokens == nil || *fromMaxTokens.Options.MaxOutputTokens != 64000 {
		t.Fatalf("request.MaxTokens = %v, want 64000", fromMaxTokens.Options.MaxOutputTokens)
	}

	unset, _, err := convertRequest(hyprovider.Request{Model: "deepseek-v4-pro"}, "", "deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if unset.Options.MaxOutputTokens != nil {
		t.Fatalf("unset max output should leave option nil, got %v", *unset.Options.MaxOutputTokens)
	}
}

func TestStopReasonMapsLengthToMaxTurns(t *testing.T) {
	if got := stopReason(sdk.FinishLength); got != hyprovider.StopReasonMaxTurns {
		t.Fatalf("FinishLength stop reason = %q, want %q", got, hyprovider.StopReasonMaxTurns)
	}
	stream := &streamAdapter{inner: &sliceStream{parts: []sdk.Part{
		{Kind: sdk.PartFinish, FinishReason: sdk.FinishLength, Usage: sdk.Usage{OutputTokens: 4096}},
	}}}
	done, err := stream.Recv()
	if err != nil || done.Kind != hyprovider.EventDone || done.StopReason != hyprovider.StopReasonMaxTurns {
		t.Fatalf("length finish event = %+v, error = %v", done, err)
	}
}

func TestAnthropicCompatibleProviderUsesMessagesProtocol(t *testing.T) {
	profile, ok := compat.Lookup("alibaba")
	if !ok || profile.Protocol != compat.ProtocolAnthropic {
		t.Fatalf("alibaba protocol = %q, found = %v", profile.Protocol, ok)
	}
	provider, err := newProvider(Config{ProviderID: "alibaba", APIKey: "test-key", BaseURL: "https://example.com/anthropic"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provider.(*anthropic.Provider); !ok {
		t.Fatalf("alibaba provider = %T, want *anthropic.Provider", provider)
	}
}

func TestAnthropicCompatibleProviderUsesConfiguredOutputLimit(t *testing.T) {
	gotMaxTokens := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		gotMaxTokens = int(body["max_tokens"].(float64))
		_, _ = fmt.Fprint(response, `{"id":"msg-1","model":"deepseek-v4-flash","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()

	provider, err := newProvider(Config{
		ProviderID: "deepseek", APIKey: "test-key", BaseURL: server.URL,
		MaxOutputTokens: 384000, Client: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	model, err := provider.LanguageModel("deepseek-v4-flash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = model.Generate(context.Background(), sdk.Request{
		Messages: []sdk.Message{sdk.TextMessage(sdk.RoleUser, "hello")},
	}); err != nil {
		t.Fatal(err)
	}
	if gotMaxTokens != 384000 {
		t.Fatalf("wire max_tokens = %d, want 384000", gotMaxTokens)
	}
}

func TestTextOnlyModelOmitsHistoricalImages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(path, testImagePNG(), 0o600); err != nil {
		t.Fatal(err)
	}
	historical := message.NewText(message.RoleUser, "look at this")
	historical.Metadata = map[string]string{
		"azem.attachments": `[{"id":"img1","name":"shot.png","mime":"image/png","path":` + jsonString(path) + `}]`,
	}
	converted, _, err := convertRequest(hyprovider.Request{
		Model: "deepseek-v4-flash",
		Messages: []message.Message{
			historical,
			message.NewText(message.RoleAssistant, "I saw it."),
			message.NewText(message.RoleUser, "continue without the image"),
		},
		ExtraBody: map[string]any{
			responses.AttachmentRootExtraKey: dir,
			disableImageInputExtraKey:        true,
		},
	}, "", "opencode-go")
	if err != nil {
		t.Fatal(err)
	}
	if len(converted.Messages) != 3 || len(converted.Messages[0].Content) != 1 {
		t.Fatalf("converted messages = %+v", converted.Messages)
	}
	part := converted.Messages[0].Content[0]
	if part.Kind != sdk.ContentText || !strings.Contains(part.Text, omittedImageNotice) {
		t.Fatalf("historical image part = %+v, want text omission notice", part)
	}
}

func TestTextOnlyModelRejectsCurrentImageLocally(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(path, testImagePNG(), 0o600); err != nil {
		t.Fatal(err)
	}
	current := message.NewText(message.RoleUser, "look at this")
	current.Metadata = map[string]string{
		"azem.attachments": `[{"id":"img1","name":"shot.png","mime":"image/png","path":` + jsonString(path) + `}]`,
	}
	_, _, err := convertRequest(hyprovider.Request{
		Model:    "deepseek-v4-flash",
		Messages: []message.Message{current},
		ExtraBody: map[string]any{
			responses.AttachmentRootExtraKey: dir,
			disableImageInputExtraKey:        true,
		},
	}, "", "opencode-go")
	if err == nil || !strings.Contains(err.Error(), "does not support image input") {
		t.Fatalf("convertRequest error = %v, want local image capability rejection", err)
	}
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func testImagePNG() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde,
	}
}
