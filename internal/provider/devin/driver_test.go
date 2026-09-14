package devin

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
)

func frame(flag byte, data []byte) []byte {
	return append(binary.BigEndian.AppendUint32([]byte{flag}, uint32(len(data))), data...)
}

func TestRejectionTrailerKeepsBoundedDetailWithoutCredentials(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const token = "devin-session-token$private-token"
	const jwt = "private-jwt"
	raw, _ := json.Marshal(map[string]any{"error": map[string]string{"code": "invalid_argument", "message": "invalid tool name trace-123 " + token + " " + jwt + strings.Repeat("x", 1000)}})
	s := &eventStream{ctx: ctx, cancel: cancel, body: io.NopCloser(bytes.NewReader(frame(2, raw))), secrets: []string{token, jwt}}
	_, err := s.Recv()
	var failure *hyprovider.Error
	if !errors.As(err, &failure) || failure.StatusCode != 400 || failure.Code != "invalid_argument" || !strings.Contains(failure.Message, "trace-123") || len([]rune(failure.Message)) > 512 {
		t.Fatalf("failure=%v", err)
	}
	if strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), jwt) {
		t.Fatal("credential leaked")
	}
	if _, err := s.Recv(); err != io.EOF {
		t.Fatalf("rejection did not terminate: %v", err)
	}
}

func TestPersonalCatalogAndTwoTurnToolConversation(t *testing.T) {
	const token = "devin-session-token$test-session"
	var turns []*protoMessage
	var cascade string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if r.URL.Path == chatPath {
			if r.Header.Get("Content-Type") != "application/connect+proto" || len(body) < 5 || body[0] != 1 || int(binary.BigEndian.Uint32(body[1:5])) != len(body)-5 {
				t.Error("invalid Connect request")
				return
			}
			body, err = decompress(body[5:])
			if err != nil {
				t.Error(err)
				return
			}
		}
		request := decodeProto(body, nil)
		meta := request.child(1)
		if meta.text(3) != token || r.Header.Get("Connect-Protocol-Version") != "1" {
			t.Error("missing authenticated CLI metadata")
		}
		if r.URL.Path == catalogPath {
			if meta.text(1) != "chisel" || meta.text(7) != "0.0.0-dev" || len(meta.values(30, 0)) != 5 {
				t.Error("wrong catalog identity")
			}
			var features, info, model, hidden, response proto
			features.number(11, 1)
			features.number(12, 1)
			features.number(15, 1)
			features.number(21, 1)
			info.data(6, features)
			info.number(13, 32000)
			info.number(22, 3)
			model.text(1, "Adaptive")
			model.text(22, "adaptive")
			model.number(18, 200000)
			model.data(23, info)
			hidden.text(22, "disabled")
			hidden.number(4, 1)
			response.data(1, model)
			response.data(1, hidden)
			_, _ = w.Write(response)
			return
		}
		if meta.text(1) != "devin-cli" || meta.text(28) != "chisel" || meta.text(7) != "3000.6.2" {
			t.Error("wrong chat identity")
		}
		switch r.URL.Path {
		case authPath:
			// Golden protobuf bytes: GetUserJwtResponse.userJwt (field 1).
			_, _ = w.Write([]byte{0x0a, 3, 'j', 'w', 't'})
		case assignPath:
			if request.text(2) != "adaptive" || request.child(5).text(3) != "Read the file" {
				t.Error("router did not receive current user action")
			}
			if cascade == "" {
				cascade = request.text(3)
			} else if cascade != request.text(3) {
				t.Error("conversation affinity changed")
			}
			var assignment, response proto
			assignment.text(1, "assignment")
			assignment.text(2, "actual-model")
			response.data(1, assignment)
			_, _ = w.Write(response)
		case chatPath:
			if request.text(21) != "actual-model" || request.text(26) != "assignment" || request.text(16) != cascade || meta.text(21) != "jwt" || request.text(2) != "Stable root" {
				t.Error("assignment or root context lost")
			}
			if request.number(7) != 5 || request.number(20) != 1 || request.child(10).text(1) != "read" || request.number(11) != 0 {
				t.Error("tool request configuration lost")
			}
			turns = append(turns, request)
			var delta proto
			delta.text(1, "bot-stable")
			delta.text(23, "actual-model")
			if len(turns) == 1 {
				delta.text(9, "checking")
				delta.text(10, "signed")
				delta.text(21, "anthropic")
				delta.text(3, "Reading now.")
				delta.text(25, "commentary")
				var call proto
				call.text(1, "call-1")
				call.text(2, "read")
				call.text(3, `{"path":`)
				delta.data(6, call)
				_, _ = w.Write(frame(0, delta))
				delta = nil
				call = nil
				call.text(3, `{"path":"file.txt"}`)
				delta.data(6, call)
				delta.number(5, 10)
			} else {
				prompts := request.values(3, 2)
				if len(prompts) != 4 {
					t.Errorf("history length = %d", len(prompts))
					return
				}
				late := decodeProto(prompts[1].data, nil)
				assistant := decodeProto(prompts[2].data, nil)
				tool := decodeProto(prompts[3].data, nil)
				if assistant.child(6).text(2) != "read" {
					t.Error("replayed tool name was not mapped back to its wire name")
				}
				if late.number(2) != 1 || late.text(3) != "[Trusted host context]\nPrivate tail" || assistant.text(1) != "bot-stable" || assistant.text(11) != "checking" || assistant.text(12) != "signed" || assistant.text(18) != "anthropic" || tool.number(2) != 4 || tool.text(7) != "call-1" || tool.text(3) != "file contents" {
					t.Error("multi-turn context, signature or tool result lost")
				}
				if decodeProto(turns[0].values(3, 2)[0].data, nil).text(1) != decodeProto(prompts[0].data, nil).text(1) {
					t.Error("history message ID changed")
				}
				delta.text(3, "Done.")
				delta.text(25, "final_answer")
				delta.number(5, 2)
			}
			var usage proto
			usage.number(2, 10)
			usage.number(3, 5)
			usage.number(4, 20)
			usage.number(5, 100)
			delta.data(7, usage)
			var compressed bytes.Buffer
			zw := gzip.NewWriter(&compressed)
			_, _ = zw.Write(delta)
			_ = zw.Close()
			_, _ = w.Write(frame(1, compressed.Bytes()))
			_, _ = w.Write(frame(2, []byte(`{}`)))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	models, err := fetchModels(context.Background(), server.Client(), server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "adaptive" || !models[0].DevinRouter || !models[0].SupportsParallel || models[0].MaxOutputTokens != 32000 {
		t.Fatalf("catalog = %+v", models)
	}
	driver, err := New(func(context.Context) (string, error) { return token, nil }, "personal", models[0])
	if err != nil {
		t.Fatal(err)
	}
	driver.client, driver.baseURL = server.Client(), server.URL
	request := hyprovider.Request{Model: "adaptive", PromptCacheKey: "session", Messages: []message.Message{{Role: message.RoleSystem, Text: "Stable root"}, {Role: message.RoleUser, Text: "Read the file"}, {Role: message.RoleSystem, Text: "Private tail"}}, Tools: []message.ToolDefinition{{Name: "coding.read_file", InputSchema: message.JSONSchema{Type: "object"}}}}
	for turn := 0; turn < 2; turn++ {
		stream, err := driver.Stream(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		var done hyprovider.Event
		var calls []message.ToolCall
		var thinking, signature, text string
		for {
			e, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			switch e.Kind {
			case hyprovider.EventTextDelta:
				text += e.Text
				want := hyprovider.TextPhaseCommentary
				if turn == 1 {
					want = hyprovider.TextPhaseFinalAnswer
				}
				if e.TextPhase != want {
					t.Error("text phase lost")
				}
			case hyprovider.EventThinkingDelta:
				thinking += e.Thinking
				signature += e.Signature
			case hyprovider.EventToolCall:
				calls = append(calls, *e.ToolCall)
			case hyprovider.EventDone:
				done = e
			}
		}
		if done.Kind != hyprovider.EventDone || done.Usage.InputTokens != 130 || done.Usage.CachedInputTokens != 100 || !done.Usage.CacheWriteInputTokensReported || done.Usage.TotalTokens != 135 {
			t.Fatalf("completion/usage = %+v", done)
		}
		if turn == 0 {
			if len(calls) != 1 || string(calls[0].Arguments) != `{"path":"file.txt"}` || done.StopReason != hyprovider.StopReasonToolUse {
				t.Fatalf("tool completion=%+v", calls)
			}
			request.Messages = append(request.Messages, message.Message{Role: message.RoleAssistant, Content: []message.ContentPart{{Kind: message.ContentText, Text: text}, {Kind: message.ContentReasoning, Text: thinking, Signature: signature}}, ToolCalls: calls, ProviderState: done.ProviderState}, message.Message{Role: message.RoleTool, ToolResult: &message.ToolResult{ToolCallID: "call-1", Content: "file contents"}})
		} else if text != "Done." || len(calls) != 0 {
			t.Fatal("second turn replayed tools or lost final answer")
		}
	}
}

func TestStreamRejectsIncompleteAndUntrustedToolResponses(t *testing.T) {
	var call, delta proto
	call.text(1, "call")
	call.text(2, "read")
	call.text(3, `{"path":"x"}`)
	delta.data(6, call)
	for name, tail := range map[string][]byte{
		"truncated":         nil,
		"trailer-error":     frame(2, []byte(`{"error":{"code":"permission_denied","message":"secret"}}`)),
		"malformed-trailer": frame(2, []byte(`{`)),
		"length":            append(frame(0, []byte{0x28, 3}), frame(2, []byte(`{}`))...),
		"content-filter":    append(frame(0, []byte{0x28, 11}), frame(2, []byte(`{}`))...),
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &eventStream{ctx: ctx, cancel: cancel, body: io.NopCloser(bytes.NewReader(append(frame(0, delta), tail...))), calls: map[string]*message.ToolCall{}, tools: []message.ToolDefinition{{Name: "read"}}, secrets: []string{"secret"}}
			for {
				e, err := s.Recv()
				if err != nil {
					if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "secret") {
						t.Fatalf("unsafe successful/error response %v", err)
					}
					break
				}
				if e.Kind == hyprovider.EventToolCall || e.Kind == hyprovider.EventDone {
					t.Fatal("incomplete stream finalized tool")
				}
			}
			if _, err := s.Recv(); !errors.Is(err, io.EOF) {
				t.Fatal("stream did not stop after error")
			}
		})
	}
	for _, args := range []string{`{`, `[]`, `null`, `{}`} {
		ctx, cancel := context.WithCancel(context.Background())
		s := &eventStream{ctx: ctx, cancel: cancel, body: io.NopCloser(strings.NewReader("")), calls: map[string]*message.ToolCall{"call": {ID: "call", Name: "unknown", Arguments: json.RawMessage(args)}}, order: []string{"call"}}
		if s.finish() == nil {
			t.Error("accepted unknown or invalid call")
		}
		s.Close()
	}
}

func TestProtocolBoundsAccountIsolationAndCatalogFailure(t *testing.T) {
	for _, raw := range [][]byte{{0}, {0x0a, 0xff}, {0x0f}, bytes.Repeat([]byte{8, 1}, maxProtoFields+1)} {
		if decodeProto(raw, nil).err == nil {
			t.Error("accepted malformed/oversized protobuf")
		}
	}
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, _ = zw.Write(bytes.Repeat([]byte("x"), maxFrameBytes+1))
	_ = zw.Close()
	if _, err := decompress(compressed.Bytes()); err == nil {
		t.Fatal("unbounded compressed response")
	}
	for _, custom := range []string{"http://server.codeium.com", "https://server.codeium.com.evil.test", "https://evil.test", "https://user@server.codeium.com", "https://server.codeium.com/path"} {
		if _, err := apiServer(DefaultAPIURL, custom); err == nil {
			t.Fatalf("trusted unsafe host %s", custom)
		}
	}
	request := hyprovider.Request{Model: "m", PromptCacheKey: "same-session"}
	one, two := conversationID("a", request), conversationID("b", request)
	if one == two || one != conversationID("a", request) {
		t.Fatal("conversation IDs not account-scoped and stable")
	}
	state, _ := json.Marshal(turnState{Provider: "devin", Model: "m", CascadeID: one, MessageID: "native-id", SignatureType: "signed"})
	request.Messages = []message.Message{{Role: message.RoleAssistant, Content: []message.ContentPart{{Kind: message.ContentReasoning, Text: "private", Signature: "signature"}, {Kind: message.ContentText, Text: "prose"}}, ProviderState: state}}
	_, prompts, err := buildPrompts(context.Background(), request, two, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeProto(prompts[0], nil); got.text(11) != "" || got.text(12) != "" || got.text(1) == "native-id" || got.text(3) != "prose" {
		t.Fatal("cross-account native reasoning replayed or prose lost")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	if _, err := fetchModels(context.Background(), server.Client(), server.URL, "session"); err == nil {
		t.Error("empty catalog returned success")
	}
	if _, err := New(nil, "", catalog.Model{}); err == nil {
		t.Error("missing account accepted")
	}
}
