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
	"reflect"
	"strings"
	"testing"

	sdk "github.com/Viking602/llmux"
	"github.com/Viking602/llmux/provider/anthropic"
	"github.com/Viking602/llmux/provider/openai/compat"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
)

type llmuxTestRequestHost struct{ root string }

func (host llmuxTestRequestHost) AttachmentRoot() string { return host.root }
func (llmuxTestRequestHost) ExecuteNativeTool(context.Context, message.ToolCall) (message.ToolResult, error) {
	return message.ToolResult{}, nil
}

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
		if profile.ID == "chatgpt" || profile.ID == "grok" || profile.ID == "cursor" {
			t.Fatalf("reserved Azem provider leaked into llmux settings: %q", profile.ID)
		}
		foundOpenAI = foundOpenAI || profile.ID == "openai"
		foundOpenRouter = foundOpenRouter || profile.ID == "openrouter"
	}
	if !foundOpenAI || !foundOpenRouter {
		t.Fatalf("missing expected profiles: openai=%v openrouter=%v", foundOpenAI, foundOpenRouter)
	}
	if _, ok := LookupProfile("cursor"); ok {
		t.Fatal("cursor leaked into llmux profiles")
	}
	if profile, ok := LookupProfile("opencode"); !ok || profile.ID != "opencode-zen" || profile.BaseURL != "https://opencode.ai/zen/v1" {
		t.Fatalf("opencode profile = %+v, found=%v", profile, ok)
	}
	stream := &streamAdapter{inner: &sliceStream{parts: []sdk.Part{
		{Kind: sdk.PartTextDelta, Delta: "hello"},
		{Kind: sdk.PartFinish, FinishReason: sdk.FinishStop, Usage: sdk.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}},
	}}}
	text, err := stream.Recv()
	if err != nil || text.Kind != hyprovider.EventTextDelta || text.Text != "hello" || text.TextPhase != hyprovider.TextPhaseFinalAnswer {
		t.Fatalf("text event = %+v, error = %v", text, err)
	}
	done, err := stream.Recv()
	if err != nil || done.Kind != hyprovider.EventDone || done.StopReason != hyprovider.StopReasonComplete || done.Usage.TotalTokens != 5 {
		t.Fatalf("done event = %+v, error = %v", done, err)
	}
}

func TestStreamPreservesNormalizedCacheUsage(t *testing.T) {
	for _, test := range []struct {
		name                  string
		usage                 sdk.Usage
		wantInput, wantCached int
		wantTotal             int
		wantReported          bool
	}{
		{
			name: "cache hit",
			usage: sdk.Usage{
				InputTokens: 8_944, CachedInputTokens: 4_608, CachedInputTokensReported: true,
				OutputTokens: 20, TotalTokens: 8_964,
			},
			wantInput: 8_944, wantCached: 4_608, wantTotal: 8_964, wantReported: true,
		},
		{
			name: "explicit zero",
			usage: sdk.Usage{
				InputTokens: 8_499, CachedInputTokensReported: true,
				OutputTokens: 20, TotalTokens: 8_519,
			},
			wantInput: 8_499, wantTotal: 8_519, wantReported: true,
		},
		{
			name:      "unsupported cache",
			usage:     sdk.Usage{InputTokens: 4_336, OutputTokens: 20, TotalTokens: 4_356},
			wantInput: 4_336, wantTotal: 4_356,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			stream := &streamAdapter{inner: &sliceStream{parts: []sdk.Part{{
				Kind: sdk.PartFinish, FinishReason: sdk.FinishStop, Usage: test.usage,
			}}}}
			done, err := stream.Recv()
			if err != nil {
				t.Fatal(err)
			}
			if done.Usage.InputTokens != test.wantInput ||
				done.Usage.CachedInputTokens != test.wantCached ||
				done.Usage.TotalTokens != test.wantTotal ||
				done.Usage.CachedInputTokensReported != test.wantReported {
				t.Fatalf("usage = %#v", done.Usage)
			}
		})
	}
}

func TestConvertRequestHonorsTypedPortableOptions(t *testing.T) {
	parallel := false
	converted, _, err := convertRequest(hyprovider.Request{
		Model: "deepseek-v4-pro", MaxTokens: 384000,
		PromptCacheKey: "session-cache", ServiceTier: "priority", ParallelToolCalls: &parallel,
	}, "", "deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if converted.Options.MaxOutputTokens == nil || *converted.Options.MaxOutputTokens != 384000 ||
		converted.Options.PromptCacheKey != "session-cache" || converted.Options.ServiceTier != "priority" ||
		converted.Options.ParallelToolCalls == nil || *converted.Options.ParallelToolCalls {
		t.Fatalf("portable options = %#v", converted.Options)
	}

	unset, _, err := convertRequest(hyprovider.Request{Model: "deepseek-v4-pro"}, "", "deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if unset.Options.MaxOutputTokens != nil {
		t.Fatalf("unset max output should leave option nil, got %v", *unset.Options.MaxOutputTokens)
	}
}

func TestStopReasonMapsLengthDistinctly(t *testing.T) {
	if got := stopReason(sdk.FinishLength); got != hyprovider.StopReasonLength {
		t.Fatalf("FinishLength stop reason = %q, want %q", got, hyprovider.StopReasonLength)
	}
	stream := &streamAdapter{inner: &sliceStream{parts: []sdk.Part{
		{Kind: sdk.PartFinish, FinishReason: sdk.FinishLength, Usage: sdk.Usage{OutputTokens: 4096}},
	}}}
	done, err := stream.Recv()
	if err != nil || done.Kind != hyprovider.EventDone || done.StopReason != hyprovider.StopReasonLength {
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

func TestAnthropicConversionKeepsLatePrivateSystemContextInMessageTail(t *testing.T) {
	lateSystem := message.NewText(message.RoleSystem, "dynamic trusted context")
	lateSystem.Visibility = message.VisibilityPrivate
	converted, _, err := convertRequest(hyprovider.Request{
		Model: "deepseek-v4-flash",
		Messages: []message.Message{
			message.NewText(message.RoleSystem, "stable core instructions"),
			message.NewText(message.RoleUser, "stable long prefix"),
			message.NewText(message.RoleAssistant, "prior answer"),
			lateSystem,
			message.NewText(message.RoleUser, "new question"),
		},
	}, "", "deepseek")
	if err != nil {
		t.Fatal(err)
	}
	type requestPrefix struct {
		instructions string
		messages     []sdk.Message
	}
	got := requestPrefix{instructions: converted.Instructions, messages: converted.Messages}
	want := requestPrefix{
		instructions: "stable core instructions",
		messages: []sdk.Message{
			sdk.TextMessage(sdk.RoleUser, "stable long prefix"),
			sdk.TextMessage(sdk.RoleAssistant, "prior answer"),
			sdk.TextMessage(sdk.RoleUser, trustedHostContextPrefix+"dynamic trusted context"),
			sdk.TextMessage(sdk.RoleUser, "new question"),
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("converted request prefix = %+v, want %+v", got, want)
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
		NativeToolHost: llmuxTestRequestHost{root: dir},
		ExtraBody:      map[string]any{disableImageInputExtraKey: true},
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
		Model:          "deepseek-v4-flash",
		Messages:       []message.Message{current},
		NativeToolHost: llmuxTestRequestHost{root: dir},
		ExtraBody:      map[string]any{disableImageInputExtraKey: true},
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
