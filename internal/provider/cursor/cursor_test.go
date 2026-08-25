package cursor

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
)

type cursorTestRequestHost struct{ root string }

func (host cursorTestRequestHost) AttachmentRoot() string { return host.root }
func (cursorTestRequestHost) ExecuteNativeTool(context.Context, message.ToolCall) (message.ToolResult, error) {
	return message.ToolResult{}, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	if request != nil && request.Body != nil {
		go io.Copy(io.Discard, request.Body)
	}
	return fn(request)
}

func TestDecodeUsableModelsParsesOfficialTierContextAndMaxMode(t *testing.T) {
	details := encodeString(nil, fieldModelID, "claude-opus-4-8-high-fast")
	details = encodeString(details, fieldModelDisplayName, "Claude Opus 4.8 1M Fast")
	details = encodeString(details, fieldModelAlias, "claude-opus-latest")
	details = encodeMessage(details, fieldModelThinking, nil)
	details = encodeUint32(details, fieldModelMaxMode, 1)
	payload := encodeMessage(nil, 1, details)
	models, err := decodeUsableModels(payload)
	if err != nil || len(models) != 1 {
		t.Fatalf("models=%+v err=%v", models, err)
	}
	model := models[0]
	if model.ID != "claude-opus-4-8-high-fast" || model.Name != "Claude Opus 4.8 1M Fast" ||
		model.ContextWindow != 1_000_000 || !model.CursorMaxMode || !model.SupportsReasoning ||
		strings.Join(model.ReasoningLevels, ",") != "high" {
		t.Fatalf("model=%+v", model)
	}
}

func TestDriverMetadataVersionsMaxModeWireContract(t *testing.T) {
	driver, err := New(func(context.Context) (string, error) { return "access", nil }, "account-1", []string{"claude-opus-4-8-high-fast"}, true, NewConversationCache())
	if err != nil {
		t.Fatal(err)
	}
	if metadata := driver.Metadata(); metadata.Version != "3" {
		t.Fatalf("Cursor transport version = %q", metadata.Version)
	}
}

func TestDecodeFieldsRejectsOverflowLengthWithoutPanic(t *testing.T) {
	payload := encodeKey(nil, 1, 2)
	payload = append(payload, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01)
	if _, err := decodeFields(payload); err == nil {
		t.Fatal("overflowing protobuf length was accepted")
	}
}

func TestEncodeMessagePreservesEmptyPresence(t *testing.T) {
	fields, err := decodeFields(encodeMessage(nil, fieldAgentHeartbeat, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !hasField(fields, fieldAgentHeartbeat) {
		t.Fatal("empty protobuf message presence was dropped")
	}
}

func TestNextConnectFrameRejectsOversizeAndConsumesEnd(t *testing.T) {
	header := make([]byte, 5)
	binary.BigEndian.PutUint32(header[1:], uint32(maxConnectFrameBytes+1))
	if _, _, _, err := nextConnectFrame(header); err == nil {
		t.Fatal("oversized Connect frame was accepted")
	}
	end := encodeConnectFrame([]byte(`{}`), true)
	_, rest, ok, err := nextConnectFrame(end)
	if err != nil || ok || len(rest) != 0 {
		t.Fatalf("end frame: ok=%v rest=%d err=%v", ok, len(rest), err)
	}
}

func TestNextConnectFrameReturnsTypedResourceExhausted(t *testing.T) {
	end := encodeConnectFrame([]byte(`{"error":{"code":"resource_exhausted","message":"conversation capacity"}}`), true)
	_, rest, ok, err := nextConnectFrame(end)
	var providerErr *hyprovider.Error
	if ok || len(rest) != 0 || !errors.As(err, &providerErr) {
		t.Fatalf("end error: ok=%v rest=%d err=%v", ok, len(rest), err)
	}
	if providerErr.Code != "resource_exhausted" || providerErr.Kind != hyprovider.ErrorServer {
		t.Fatalf("typed Connect error = %+v", providerErr)
	}
}

func TestConversationCacheRotatesPoisonedWireIDOnce(t *testing.T) {
	cache := NewConversationCache()
	cacheKey := conversationCacheKey("account-1", "session-1")
	firstWire, firstEntry := cache.resolve(cacheKey, "session-1")
	checkpoint := encodeBytes(nil, fieldStateTodos, []byte("state"))
	if err := firstEntry.saveCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	if !cache.rotate(cacheKey, firstWire) {
		t.Fatal("first resource-exhausted rotation was rejected")
	}
	secondWire, secondEntry := cache.resolve(cacheKey, "session-1")
	if secondWire == firstWire || secondEntry != firstEntry {
		t.Fatalf("rotation lost state: first=%q second=%q sameEntry=%v", firstWire, secondWire, secondEntry == firstEntry)
	}
	if cache.rotate(cacheKey, secondWire) {
		t.Fatal("poisoned conversation rotated more than once")
	}
}

func TestCancelledStreamDoesNotCommitBufferedCheckpoint(t *testing.T) {
	entry := &conversationEntry{blobs: &blobStore{}}
	initial := encodeBytes(nil, fieldStateTodos, []byte("initial"))
	if err := entry.saveCheckpoint(initial); err != nil {
		t.Fatal(err)
	}
	candidate := encodeBytes(nil, fieldStateTodos, []byte("candidate"))
	frame := encodeMessage(nil, fieldAgentCheckpoint, candidate)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	driver := &Driver{}
	err := driver.handleFrame(ctx, frame, nil, entry.blobs, entry, nil, nil, nil, make(chan hyprovider.Event, 1), &cursorStreamUsage{}, new(bool))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled frame error = %v", err)
	}
	entry.mu.Lock()
	stored := append([]byte(nil), entry.checkpoint...)
	entry.mu.Unlock()
	if !bytes.Equal(stored, initial) {
		t.Fatalf("cancelled checkpoint committed: %x", stored)
	}
	full := make(chan hyprovider.Event, 1)
	full <- hyprovider.Event{Kind: hyprovider.EventTextDelta}
	if sendCursorEvent(ctx, full, hyprovider.Event{Kind: hyprovider.EventTextDelta}) {
		t.Fatal("cancelled stream blocked on a full event channel")
	}
}

func TestBuildRunRequestEncodesUserTextModelAndMaxMode(t *testing.T) {
	payload, err := buildRunRequest(hyprovider.Request{
		Model: "claude-opus-4-8-high-fast",
		Messages: []message.Message{
			message.NewText(message.RoleSystem, "rules"),
			message.NewText(message.RoleUser, "hello cursor"),
		},
		Metadata:  map[string]string{"session_id": "session-1"},
		ExtraBody: map[string]any{cursorMaxModeExtraKey: true},
	}, "session-1", "account-1", NewConversationCache())
	if err != nil {
		t.Fatal(err)
	}
	if payload.ConversationID != "session-1" || payload.Resume || !bytes.Contains(payload.Body, []byte("claude-opus-4-8-high-fast")) || !bytes.Contains(payload.Body, []byte("hello cursor")) {
		t.Fatalf("payload = resume=%v body=%q", payload.Resume, payload.Body)
	}
	outer, err := decodeFields(payload.Body)
	if err != nil {
		t.Fatal(err)
	}
	run, err := decodeFields(fieldBytes(outer, fieldAgentRunRequest))
	if err != nil {
		t.Fatal(err)
	}
	details, err := decodeFields(fieldBytes(run, fieldRunModelDetails))
	if err != nil {
		t.Fatal(err)
	}
	requested, err := decodeFields(fieldBytes(run, fieldRunRequestedModel))
	if err != nil {
		t.Fatal(err)
	}
	if fieldUint32(details, fieldModelMaxMode) != 1 || fieldUint32(requested, fieldRequestedMaxMode) != 1 {
		t.Fatalf("max mode missing: details=%v requested=%v", details, requested)
	}
}

func TestBuildRunRequestPreservesImageOnlyUserAttachment(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "image.png")
	image := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0, 'I', 'H', 'D', 'R'}
	if err := os.WriteFile(path, image, 0o600); err != nil {
		t.Fatal(err)
	}
	attachments, err := json.Marshal([]map[string]any{{
		"id": "image-1", "name": "image.png", "mime": "image/png", "path": path, "size": len(image),
	}})
	if err != nil {
		t.Fatal(err)
	}
	user := message.NewText(message.RoleUser, "")
	user.Metadata = map[string]string{"azem.attachments": string(attachments)}
	payload, err := buildRunRequest(hyprovider.Request{
		Model: "composer-2", Messages: []message.Message{user},
		NativeToolHost: cursorTestRequestHost{root: root},
	}, "session-1", "account-1", NewConversationCache())
	if err != nil {
		t.Fatal(err)
	}
	if payload.Resume {
		t.Fatal("image-only Cursor request became resume")
	}
	envelope, err := decodeFields(payload.Body)
	if err != nil {
		t.Fatal(err)
	}
	run, err := decodeFields(fieldBytes(envelope, fieldAgentRunRequest))
	if err != nil {
		t.Fatal(err)
	}
	action, err := decodeFields(fieldBytes(run, fieldRunAction))
	if err != nil {
		t.Fatal(err)
	}
	userAction, err := decodeFields(fieldBytes(action, fieldActionUserMessage))
	if err != nil {
		t.Fatal(err)
	}
	userMessage, err := decodeFields(fieldBytes(userAction, fieldUserMessageActionMsg))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := decodeFields(fieldBytes(userMessage, fieldUserMessageSelectedContext))
	if err != nil {
		t.Fatal(err)
	}
	images := fieldAllBytes(selected, fieldSelectedContextImages)
	if len(images) != 1 {
		t.Fatalf("selected images = %d", len(images))
	}
	selectedImage, err := decodeFields(images[0])
	if err != nil {
		t.Fatal(err)
	}
	if fieldString(selectedImage, fieldSelectedImageMIME) != "image/png" ||
		!bytes.Equal(fieldBytes(selectedImage, fieldSelectedImageData), image) {
		t.Fatalf("selected image fields = %v", selectedImage)
	}
}

func TestBuildRunRequestUsesPromptCacheKeyAsConversationID(t *testing.T) {
	request := hyprovider.Request{
		Model:          "composer-2",
		Messages:       []message.Message{message.NewText(message.RoleUser, "hello")},
		PromptCacheKey: "session-cache",
	}
	cache := NewConversationCache()
	first, err := buildRunRequest(request, "", "account-1", cache)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildRunRequest(request, "", "account-1", cache)
	if err != nil {
		t.Fatal(err)
	}
	if first.ConversationID != "session-cache" || second.ConversationID != first.ConversationID {
		t.Fatalf("conversation IDs = %q, %q", first.ConversationID, second.ConversationID)
	}
}

func TestBuildRunRequestResumesAfterToolResultAndPreservesMessageOrder(t *testing.T) {
	assistant := message.NewText(message.RoleAssistant, "")
	assistant.ToolCalls = []message.ToolCall{{ID: "call-1", Name: "coding.read_file", Arguments: json.RawMessage(`{"path":"README.md"}`)}}
	request := hyprovider.Request{
		Model: "composer-2",
		Messages: []message.Message{
			message.NewText(message.RoleSystem, "stable rules"),
			message.NewText(message.RoleUser, "inspect the repository"),
			message.NewText(message.RoleSystem, "late trusted context"),
			assistant,
			message.NewToolResult(message.ToolResult{ToolCallID: "call-1", Name: "coding.read_file", Content: "contents"}),
		},
	}
	payload, err := buildRunRequest(request, "conversation-1", "account-1", NewConversationCache())
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := decodeFields(payload.Body)
	if err != nil {
		t.Fatal(err)
	}
	run, err := decodeFields(fieldBytes(envelope, fieldAgentRunRequest))
	if err != nil {
		t.Fatal(err)
	}
	action, err := decodeFields(fieldBytes(run, fieldRunAction))
	if err != nil {
		t.Fatal(err)
	}
	if !hasField(action, fieldActionResume) {
		t.Fatalf("tool continuation action = %v, want resume", action)
	}
	state, err := decodeFields(fieldBytes(run, fieldRunState))
	if err != nil {
		t.Fatal(err)
	}
	var roles []string
	for _, id := range fieldAllBytes(state, fieldStateRootPromptJSON) {
		var item struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(payload.Blobs.get(id), &item); err != nil {
			t.Fatal(err)
		}
		roles = append(roles, item.Role)
	}
	want := []string{"system", "user", "system", "assistant", "tool"}
	if strings.Join(roles, ",") != strings.Join(want, ",") {
		t.Fatalf("root roles = %v, want %v", roles, want)
	}
}

func TestBuildRunRequestReplaysNativeExecProviderState(t *testing.T) {
	providerState, err := json.Marshal(cursorProviderStateV1{Version: 1, Provider: "cursor", Model: "composer-2", NativeExecs: []cursorNativeExecRecord{{
		CallID: "native-1", Name: "coding.read_file", Arguments: json.RawMessage(`{"path":"README.md"}`),
		Content: "native contents",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	assistant := message.NewText(message.RoleAssistant, "read complete")
	assistant.ProviderState = providerState
	payload, err := buildRunRequest(hyprovider.Request{
		Model: "composer-2",
		Messages: []message.Message{
			message.NewText(message.RoleSystem, "rules"),
			message.NewText(message.RoleUser, "read the file"),
			assistant,
			message.NewText(message.RoleUser, "what did it contain?"),
		},
	}, "conversation-1", "account-1", NewConversationCache())
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := decodeFields(payload.Body)
	if err != nil {
		t.Fatal(err)
	}
	run, err := decodeFields(fieldBytes(envelope, fieldAgentRunRequest))
	if err != nil {
		t.Fatal(err)
	}
	state, err := decodeFields(fieldBytes(run, fieldRunState))
	if err != nil {
		t.Fatal(err)
	}
	var roles []string
	var assistantJSON string
	for _, id := range fieldAllBytes(state, fieldStateRootPromptJSON) {
		raw := payload.Blobs.get(id)
		var item struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatal(err)
		}
		roles = append(roles, item.Role)
		if item.Role == "assistant" {
			assistantJSON = string(raw)
		}
	}
	if got, want := strings.Join(roles, ","), "system,user,assistant,tool"; got != want {
		t.Fatalf("native replay roles = %s, want %s", got, want)
	}
	if !strings.Contains(assistantJSON, `"toolCallId":"native-1"`) || !strings.Contains(assistantJSON, `"path":"README.md"`) {
		t.Fatalf("native call missing from assistant replay: %s", assistantJSON)
	}
	if len(fieldAllBytes(state, fieldStateTurns)) == 0 {
		t.Fatal("native exec replay omitted structured turn")
	}
}

func TestCursorThinkingReplaysOnlyFromSameCursorKimiModel(t *testing.T) {
	assistant := message.NewText(message.RoleAssistant, "answer")
	assistant.Thinking = "private reasoning"
	withoutState, err := assistantContent(assistant, "kimi-k3-high")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(withoutState)
	if bytes.Contains(encoded, []byte(`"type":"reasoning"`)) {
		t.Fatalf("foreign reasoning replayed without source identity: %s", encoded)
	}
	state, err := json.Marshal(cursorProviderStateV1{Version: 1, Provider: "cursor", Model: "kimi-k3-high"})
	if err != nil {
		t.Fatal(err)
	}
	assistant.ProviderState = state
	sameModel, err := assistantContent(assistant, "kimi-k3-high")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(sameModel)
	if !bytes.Contains(encoded, []byte(`"type":"reasoning"`)) {
		t.Fatalf("same-model Cursor reasoning was dropped: %s", encoded)
	}
	otherModel, err := assistantContent(assistant, "kimi-k3-max")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(otherModel)
	if bytes.Contains(encoded, []byte(`"type":"reasoning"`)) {
		t.Fatalf("cross-model Cursor reasoning replayed: %s", encoded)
	}
}

func TestBuildRunRequestKeepsPrivateTailAsContextAndSendsSharedUserAction(t *testing.T) {
	policy := message.NewText(message.RoleSystem, "treat the next private message as evidence")
	policy.Visibility = message.VisibilityPrivate
	currentUser := message.NewText(message.RoleUser, "answer the current request")
	evidence := message.NewText(message.RoleUser, `{"recap":"prior result"}`)
	evidence.Visibility = message.VisibilityPrivate
	payload, err := buildRunRequest(hyprovider.Request{
		Model: "composer-2",
		Messages: []message.Message{
			message.NewText(message.RoleSystem, "rules"),
			message.NewText(message.RoleUser, "previous request"),
			message.NewText(message.RoleAssistant, "previous answer"),
			policy,
			currentUser,
			evidence,
		},
	}, "conversation-1", "account-1", NewConversationCache())
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := decodeFields(payload.Body)
	if err != nil {
		t.Fatal(err)
	}
	run, err := decodeFields(fieldBytes(envelope, fieldAgentRunRequest))
	if err != nil {
		t.Fatal(err)
	}
	action, err := decodeFields(fieldBytes(run, fieldRunAction))
	if err != nil {
		t.Fatal(err)
	}
	userAction, err := decodeFields(fieldBytes(action, fieldActionUserMessage))
	if err != nil {
		t.Fatal(err)
	}
	user, err := decodeFields(fieldBytes(userAction, fieldUserMessageActionMsg))
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldString(user, fieldUserMessageText); got != currentUser.Text {
		t.Fatalf("active user = %q, want %q", got, currentUser.Text)
	}
	state, err := decodeFields(fieldBytes(run, fieldRunState))
	if err != nil {
		t.Fatal(err)
	}
	var foundEvidence, foundCurrent bool
	var rootTexts []string
	for _, id := range fieldAllBytes(state, fieldStateRootPromptJSON) {
		raw := payload.Blobs.get(id)
		rootTexts = append(rootTexts, string(raw))
		var item struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatal(err)
		}
		if item.Role != "user" {
			continue
		}
		var parts []cursorTextPart
		if err := json.Unmarshal(item.Content, &parts); err != nil {
			t.Fatal(err)
		}
		for _, part := range parts {
			foundEvidence = foundEvidence || part.Text == evidence.Text
			foundCurrent = foundCurrent || part.Text == currentUser.Text
		}
	}
	if !foundEvidence || foundCurrent {
		t.Fatalf("private tail/current action projection is wrong: %s", strings.Join(rootTexts, "\n"))
	}
}

func TestPrivateTailWireOrderStaysStableWhenTurnBecomesHistory(t *testing.T) {
	cache := NewConversationCache()
	policy := message.NewText(message.RoleSystem, "evidence policy")
	policy.Visibility = message.VisibilityPrivate
	firstUser := message.NewText(message.RoleUser, "first task")
	evidence := message.NewText(message.RoleUser, `{"fact":"one"}`)
	evidence.Visibility = message.VisibilityPrivate
	rootIDs := func(messages []message.Message) [][]byte {
		t.Helper()
		payload, err := buildRunRequest(hyprovider.Request{Model: "composer-2", Messages: messages}, "conversation-1", "account-1", cache)
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := decodeFields(payload.Body)
		if err != nil {
			t.Fatal(err)
		}
		run, err := decodeFields(fieldBytes(envelope, fieldAgentRunRequest))
		if err != nil {
			t.Fatal(err)
		}
		state, err := decodeFields(fieldBytes(run, fieldRunState))
		if err != nil {
			t.Fatal(err)
		}
		return fieldAllBytes(state, fieldStateRootPromptJSON)
	}
	first := rootIDs([]message.Message{
		message.NewText(message.RoleSystem, "rules"),
		policy,
		firstUser,
		evidence,
	})
	second := rootIDs([]message.Message{
		message.NewText(message.RoleSystem, "rules"),
		policy,
		firstUser,
		evidence,
		message.NewText(message.RoleAssistant, "first answer"),
		message.NewText(message.RoleUser, "second task"),
	})
	if len(second) <= len(first) {
		t.Fatalf("historical root did not grow: first=%d second=%d", len(first), len(second))
	}
	for index := range first {
		if !bytes.Equal(first[index], second[index]) {
			t.Fatalf("private-tail prefix changed at %d: first=%x second=%x", index, first[index], second[index])
		}
	}
}

func TestConversationCacheReusesCheckpointAndBlobsUntilPromptHeadChanges(t *testing.T) {
	cache := NewConversationCache()
	first, err := buildRunRequest(hyprovider.Request{
		Model: "composer-2",
		Messages: []message.Message{
			message.NewText(message.RoleSystem, "stable rules"),
			message.NewText(message.RoleUser, "first question"),
		},
	}, "conversation-1", "account-1", cache)
	if err != nil {
		t.Fatal(err)
	}
	stateOf := func(payload runPayload) []byte {
		t.Helper()
		envelope, decodeErr := decodeFields(payload.Body)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		run, decodeErr := decodeFields(fieldBytes(envelope, fieldAgentRunRequest))
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return append([]byte(nil), fieldBytes(run, fieldRunState)...)
	}
	checkpoint := stateOf(first)
	checkpoint = encodeBytes(checkpoint, fieldStateTodos, []byte("todo-blob"))
	checkpoint = encodeString(checkpoint, fieldStatePreviousWorkspace, "file:///workspace")
	serverBlobID := blobID([]byte("server state"))
	first.Blobs.set(serverBlobID, []byte("server state"))
	if err := first.Conversation.saveCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	malformed := encodeKey(nil, fieldStateTokenDetails, 2)
	malformed = append(malformed, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01)
	if err := first.Conversation.saveCheckpoint(malformed); err == nil {
		t.Fatal("malformed checkpoint replaced the last known good state")
	}

	second, err := buildRunRequest(hyprovider.Request{
		Model: "composer-2",
		Messages: []message.Message{
			message.NewText(message.RoleSystem, "stable rules"),
			message.NewText(message.RoleUser, "first question"),
			message.NewText(message.RoleAssistant, "first answer"),
			message.NewText(message.RoleUser, "second question"),
		},
	}, "conversation-1", "account-1", cache)
	if err != nil {
		t.Fatal(err)
	}
	secondState, err := decodeFields(stateOf(second))
	if err != nil {
		t.Fatal(err)
	}
	if !hasField(secondState, fieldStateTodos) || fieldString(secondState, fieldStatePreviousWorkspace) != "file:///workspace" {
		t.Fatalf("checkpoint fields were not preserved: %v", secondState)
	}
	if len(fieldAllBytes(secondState, fieldStateTurns)) == 0 {
		t.Fatal("rebuilt conversation state omitted turns")
	}
	if got := second.Blobs.get(serverBlobID); !bytes.Equal(got, []byte("server state")) {
		t.Fatalf("shared blob = %q", got)
	}

	changed, err := buildRunRequest(hyprovider.Request{
		Model: "composer-2",
		Messages: []message.Message{
			message.NewText(message.RoleSystem, "changed rules"),
			message.NewText(message.RoleUser, "second question"),
		},
	}, "conversation-1", "account-1", cache)
	if err != nil {
		t.Fatal(err)
	}
	changedState, err := decodeFields(stateOf(changed))
	if err != nil {
		t.Fatal(err)
	}
	if hasField(changedState, fieldStateTodos) || hasField(changedState, fieldStatePreviousWorkspace) {
		t.Fatalf("stale checkpoint survived prompt-head change: %v", changedState)
	}
}

func TestMultiTurnRootBlobPrefixStaysStableWhenDynamicContextAppendsAtTail(t *testing.T) {
	cache := NewConversationCache()
	rootIDs := func(messages []message.Message) [][]byte {
		t.Helper()
		payload, err := buildRunRequest(hyprovider.Request{Model: "composer-2", Messages: messages}, "conversation-1", "account-1", cache)
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := decodeFields(payload.Body)
		if err != nil {
			t.Fatal(err)
		}
		run, err := decodeFields(fieldBytes(envelope, fieldAgentRunRequest))
		if err != nil {
			t.Fatal(err)
		}
		state, err := decodeFields(fieldBytes(run, fieldRunState))
		if err != nil {
			t.Fatal(err)
		}
		return fieldAllBytes(state, fieldStateRootPromptJSON)
	}
	before := rootIDs([]message.Message{
		message.NewText(message.RoleSystem, "stable rules"),
		message.NewText(message.RoleSystem, "todo revision 1"),
		message.NewText(message.RoleUser, "first question"),
		message.NewText(message.RoleAssistant, "first answer"),
		message.NewText(message.RoleUser, "second question"),
	})
	after := rootIDs([]message.Message{
		message.NewText(message.RoleSystem, "stable rules"),
		message.NewText(message.RoleSystem, "todo revision 1"),
		message.NewText(message.RoleUser, "first question"),
		message.NewText(message.RoleAssistant, "first answer"),
		message.NewText(message.RoleSystem, "todo revision 2"),
		message.NewText(message.RoleUser, "second question"),
		message.NewText(message.RoleAssistant, "second answer"),
		message.NewText(message.RoleUser, "third question"),
	})
	if len(after) <= len(before) {
		t.Fatalf("root did not grow: before=%d after=%d", len(before), len(after))
	}
	for index := range before {
		if !bytes.Equal(before[index], after[index]) {
			t.Fatalf("root prefix changed at %d: before=%x after=%x", index, before[index], after[index])
		}
	}
}

func TestConversationCacheIsolatesIndependentPromptKeys(t *testing.T) {
	cache := NewConversationCache()
	request := hyprovider.Request{
		Model: "composer-2",
		Messages: []message.Message{
			message.NewText(message.RoleSystem, "rules"),
			message.NewText(message.RoleUser, "question"),
		},
	}
	first, err := buildRunRequest(request, "main-session", "account-1", cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Conversation.saveCheckpoint(encodeBytes(nil, fieldStateTodos, []byte("private-main-state"))); err != nil {
		t.Fatal(err)
	}
	second, err := buildRunRequest(request, "subagent-run", "account-1", cache)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := decodeFields(second.Body)
	if err != nil {
		t.Fatal(err)
	}
	run, err := decodeFields(fieldBytes(envelope, fieldAgentRunRequest))
	if err != nil {
		t.Fatal(err)
	}
	state, err := decodeFields(fieldBytes(run, fieldRunState))
	if err != nil {
		t.Fatal(err)
	}
	if hasField(state, fieldStateTodos) {
		t.Fatalf("main checkpoint leaked into subagent conversation: %v", state)
	}
}

func TestConversationCacheIsolatesAccountsWithTheSamePromptKey(t *testing.T) {
	cache := NewConversationCache()
	request := hyprovider.Request{
		Model: "composer-2",
		Messages: []message.Message{
			message.NewText(message.RoleSystem, "rules"),
			message.NewText(message.RoleUser, "question"),
		},
	}
	first, err := buildRunRequest(request, "shared-session", "account-1", cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Conversation.saveCheckpoint(encodeBytes(nil, fieldStateTodos, []byte("account-1-state"))); err != nil {
		t.Fatal(err)
	}
	second, err := buildRunRequest(request, "shared-session", "account-2", cache)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := decodeFields(second.Body)
	if err != nil {
		t.Fatal(err)
	}
	run, err := decodeFields(fieldBytes(envelope, fieldAgentRunRequest))
	if err != nil {
		t.Fatal(err)
	}
	state, err := decodeFields(fieldBytes(run, fieldRunState))
	if err != nil {
		t.Fatal(err)
	}
	if hasField(state, fieldStateTodos) || second.CacheKey == first.CacheKey {
		t.Fatalf("account-scoped checkpoint leaked: first=%q second=%q state=%v", first.CacheKey, second.CacheKey, state)
	}
}

func TestConversationEncodingPreservesEmptyJSONValuesAndToolResults(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
		field int
	}{
		{name: "object", value: map[string]any{}, field: fieldValueStruct},
		{name: "list", value: []any{}, field: fieldValueList},
		{name: "string", value: "", field: fieldValueString},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := encodeProtoValue(test.value)
			if err != nil {
				t.Fatal(err)
			}
			fields, err := decodeFields(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if !hasField(fields, test.field) {
				t.Fatalf("empty %s lost oneof presence: %v", test.name, fields)
			}
		})
	}
	result, err := decodeFields(encodeConversationToolResult(message.ToolResult{}))
	if err != nil {
		t.Fatal(err)
	}
	success, err := decodeFields(fieldBytes(result, fieldMCPToolResultSuccess))
	if err != nil {
		t.Fatal(err)
	}
	if !hasField(result, fieldMCPToolResultSuccess) || !hasField(success, fieldMCPSuccessContent) {
		t.Fatalf("empty tool result lost pairing: result=%v success=%v", result, success)
	}
}

func TestCursorProtoDecoderRejectsExcessiveFields(t *testing.T) {
	payload := make([]byte, 0, (maxCursorProtoFields+1)*2)
	for range maxCursorProtoFields + 1 {
		payload = encodeUint32(payload, 1, 1)
	}
	if _, err := decodeFields(payload); err == nil || !strings.Contains(err.Error(), "field budget") {
		t.Fatalf("field budget error = %v", err)
	}
}

func TestCursorProtoValueRejectsExcessiveNesting(t *testing.T) {
	value := encodeString(nil, fieldValueString, "leaf")
	for range maxCursorProtoValueDepth + 2 {
		value = encodeBytes(nil, fieldValueList, encodeMessage(nil, fieldListValueItem, value))
	}
	if _, err := decodeProtoValue(value); err == nil || !strings.Contains(err.Error(), "nesting exceeds") {
		t.Fatalf("nesting error = %v", err)
	}
}

func TestCursorProtoDecoderRejectsAggregateDecodedBytes(t *testing.T) {
	budget := &protoDecodeBudget{decodedBytes: maxCursorProtoDecodedBytes - 1}
	if _, err := decodeFieldsWithBudget(encodeUint32(nil, 1, 1), budget); err == nil || !strings.Contains(err.Error(), "decoded byte budget") {
		t.Fatalf("decoded byte budget error = %v", err)
	}
}

func TestDecodeTodoCompletionUsesServerSnapshot(t *testing.T) {
	item := encodeString(nil, fieldTodoItemID, "todo-1")
	item = encodeString(item, fieldTodoItemContent, "check cache")
	item = encodeUint32(item, fieldTodoItemStatus, 2)
	success := encodeMessage(nil, fieldTodoSuccessItems, item)
	success = encodeBool(success, fieldTodoSuccessMerged, true)
	result := encodeMessage(nil, fieldTodoResultSuccess, success)
	todoCall := encodeMessage(nil, fieldTodoCallResult, result)
	toolCall := encodeMessage(nil, fieldToolTodosCall, todoCall)
	completed := encodeString(nil, fieldToolCallID, "todo-call")
	completed = encodeMessage(completed, fieldToolCallBody, toolCall)
	callID, snapshot, providerError, ok, err := decodeTodoCompletion(completed)
	if err != nil || !ok || providerError != "" || callID != "todo-call" {
		t.Fatalf("Todo completion id=%q snapshot=%+v providerError=%q ok=%v err=%v", callID, snapshot, providerError, ok, err)
	}
	if !snapshot.Merged || len(snapshot.Items) != 1 || snapshot.Items[0].Status != "in_progress" || snapshot.Items[0].Content != "check cache" {
		t.Fatalf("Todo snapshot=%+v", snapshot)
	}
}

func TestMCPDefinitionsAndArgumentsUseProtoValue(t *testing.T) {
	definition := message.ToolDefinition{
		Name: "coding.lookup",
		InputSchema: message.JSONSchema{
			Type: "object",
			Properties: map[string]message.JSONSchema{
				"count": {Type: "number"},
			},
			Required: []string{"count"},
		},
	}
	fields, err := decodeFields(encodeMCPToolDef(definition))
	if err != nil {
		t.Fatal(err)
	}
	if !hasField(fields, fieldMCPDefSchema) || hasField(fields, fieldMCPDefSchemaJSON) {
		t.Fatalf("MCP schema fields = %v", fields)
	}
	schema, err := decodeProtoValue(fieldBytes(fields, fieldMCPDefSchema))
	if err != nil {
		t.Fatal(err)
	}
	schemaObject, ok := schema.(map[string]any)
	if !ok || schemaObject["type"] != "object" {
		t.Fatalf("decoded MCP schema = %#v", schema)
	}

	count, _ := encodeProtoValue(float64(3))
	enabled, _ := encodeProtoValue(true)
	nested, _ := encodeProtoValue(map[string]any{"x": "y"})
	mcpArgs := encodeString(nil, fieldMCPArgName, definition.Name)
	for key, value := range map[string][]byte{"count": count, "enabled": enabled, "nested": nested} {
		entry := encodeString(nil, fieldMapKey, key)
		entry = encodeBytes(entry, fieldMapValue, value)
		mcpArgs = encodeMessage(mcpArgs, fieldMCPArgMap, entry)
	}
	mcpArgs = encodeString(mcpArgs, fieldMCPArgToolCallID, "call-1")
	call, ok := mapMCPArgs("", encodeMessage(nil, fieldMCPArgs, mcpArgs))
	if !ok || call.Name != definition.Name || call.ID != "call-1" {
		t.Fatalf("decoded MCP call = %+v ok=%v", call, ok)
	}
	var decoded map[string]any
	if err := json.Unmarshal(call.Arguments, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["count"] != float64(3) || decoded["enabled"] != true {
		t.Fatalf("decoded MCP arguments = %#v", decoded)
	}
	nestedObject, ok := decoded["nested"].(map[string]any)
	if !ok || nestedObject["x"] != "y" {
		t.Fatalf("decoded nested MCP argument = %#v", decoded["nested"])
	}
}

func TestMCPArgumentsShareOneProtoDecodeBudget(t *testing.T) {
	value, err := encodeProtoValue(true)
	if err != nil {
		t.Fatal(err)
	}
	mcpArgs := encodeString(nil, fieldMCPArgName, "coding.lookup")
	mcpArgs = encodeString(mcpArgs, fieldMCPArgToolCallID, "call-budget")
	for range maxCursorProtoFields/3 + 1 {
		entry := encodeString(nil, fieldMapKey, "same-key")
		entry = encodeBytes(entry, fieldMapValue, value)
		mcpArgs = encodeMessage(mcpArgs, fieldMCPArgMap, entry)
	}
	if _, ok := mapMCPArgs("", encodeMessage(nil, fieldMCPArgs, mcpArgs)); ok {
		t.Fatal("MCP arguments bypassed the aggregate proto decode budget")
	}
}

func TestReplyBlobStoresSetBlobAndAcknowledges(t *testing.T) {
	data := []byte("server checkpoint blob")
	id := blobID(data)
	args := encodeBytes(nil, fieldBlobID, id)
	args = encodeBytes(args, fieldSetBlobData, data)
	serverMessage := encodeUint32(nil, fieldKVID, 7)
	serverMessage = encodeMessage(serverMessage, fieldKVSetArgs, args)
	store := &blobStore{}
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- replyBlob(writer, serverMessage, store)
		_ = writer.Close()
	}()
	outbound, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := store.get(id); !bytes.Equal(got, data) {
		t.Fatalf("stored blob = %q, want %q", got, data)
	}
	frames, err := decodeConnectFrames(outbound)
	if err != nil || len(frames) != 1 {
		t.Fatalf("ack frames=%d err=%v", len(frames), err)
	}
	envelope, err := decodeFields(frames[0])
	if err != nil {
		t.Fatal(err)
	}
	client, err := decodeFields(fieldBytes(envelope, fieldAgentKVClient))
	if err != nil {
		t.Fatal(err)
	}
	if fieldUint32(client, fieldKVID) != 7 || !hasField(client, fieldKVSetResult) {
		t.Fatalf("setBlob ack = %v", client)
	}
	result, err := decodeFields(fieldBytes(client, fieldKVSetResult))
	if err != nil || hasField(result, fieldSetBlobResultError) {
		t.Fatalf("valid setBlob returned error: fields=%v err=%v", result, err)
	}
}

func TestBlobStoreRejectsUntrustedDigestAndSize(t *testing.T) {
	store := &blobStore{}
	expectedID := blobID([]byte("expected"))
	if err := store.setRemote(expectedID, []byte("different")); err == nil {
		t.Fatal("mismatched remote blob digest was accepted")
	}
	if got := store.get(expectedID); got != nil {
		t.Fatalf("mismatched remote blob was stored: %q", got)
	}
	oversized := make([]byte, maxCursorBlobBytes+1)
	if err := store.setRemote(blobID(oversized), oversized); err == nil {
		t.Fatal("oversized remote blob was accepted")
	}
}

func TestStreamEmitsTextThinkingAndTurnEnded(t *testing.T) {
	text := encodeMessage(nil, fieldTextDelta, encodeString(nil, fieldUpdateText, "hi"))
	thinking := encodeMessage(nil, fieldThinkingDelta, encodeString(nil, fieldUpdateText, "plan"))
	ended := encodeBytes(nil, fieldTurnEnded, nil)
	frame := func(update []byte) []byte {
		return encodeConnectFrame(encodeMessage(nil, fieldAgentInteraction, update), false)
	}
	body := append(frame(text), frame(thinking)...)
	body = append(body, frame(ended)...)
	driver, err := New(func(context.Context) (string, error) { return "access", nil }, "account-1", []string{"composer-2"}, false, NewConversationCache())
	if err != nil {
		t.Fatal(err)
	}
	driver.SetClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != runPath || request.Header.Get("Authorization") != "Bearer access" {
			return &http.Response{StatusCode: http.StatusUnauthorized, Body: http.NoBody}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/connect+proto"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})})
	stream, err := driver.Stream(context.Background(), hyprovider.Request{
		Model:    "composer-2",
		Messages: []message.Message{message.NewText(message.RoleUser, "hi")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var texts, thinkingTexts []string
	var done hyprovider.Event
	for {
		event, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			t.Fatal(recvErr)
		}
		switch event.Kind {
		case hyprovider.EventTextDelta:
			texts = append(texts, event.Text)
		case hyprovider.EventThinkingDelta:
			thinkingTexts = append(thinkingTexts, event.Thinking)
		case hyprovider.EventDone:
			done = event
		}
	}
	if strings.Join(texts, "") != "hi" || strings.Join(thinkingTexts, "") != "plan" || done.StopReason != hyprovider.StopReasonComplete {
		t.Fatalf("text=%v thinking=%v done=%+v", texts, thinkingTexts, done)
	}
	state, ok, err := decodeCursorProviderState(done.ProviderState)
	if err != nil || !ok || state.Provider != "cursor" || state.Model != "composer-2" {
		t.Fatalf("Cursor done provider state=%+v ok=%v err=%v", state, ok, err)
	}
}

func TestStreamCachesCheckpointAndReportsTokenDeltaWithoutBillingCheckpointTokens(t *testing.T) {
	cache := NewConversationCache()
	systemID := blobID([]byte(`{"role":"system","content":"rules"}`))
	tokenDetails := encodeUint32(nil, fieldTokenUsed, 123)
	tokenDetails = encodeUint32(tokenDetails, fieldTokenMax, 1000)
	checkpoint := encodeBytes(nil, fieldStateRootPromptJSON, systemID)
	checkpoint = encodeBytes(checkpoint, fieldStateTodos, []byte("todo-blob"))
	checkpoint = encodeMessage(checkpoint, fieldStateTokenDetails, tokenDetails)
	tokenDelta := encodeMessage(nil, fieldTokenDelta, encodeUint32(nil, fieldUpdateTokens, 7))
	ended := encodeBytes(nil, fieldTurnEnded, nil)
	body := encodeConnectFrame(encodeMessage(nil, fieldAgentCheckpoint, checkpoint), false)
	body = append(body, encodeConnectFrame(encodeMessage(nil, fieldAgentInteraction, tokenDelta), false)...)
	body = append(body, encodeConnectFrame(encodeMessage(nil, fieldAgentInteraction, ended), false)...)

	driver, err := New(func(context.Context) (string, error) { return "access", nil }, "account-1", []string{"composer-2"}, false, cache)
	if err != nil {
		t.Fatal(err)
	}
	driver.SetClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/connect+proto"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})})
	var reportedContext ContextUsage
	stream, err := driver.Stream(context.Background(), hyprovider.Request{
		Model:          "composer-2",
		Messages:       []message.Message{message.NewText(message.RoleSystem, "rules"), message.NewText(message.RoleUser, "ask")},
		PromptCacheKey: "session-cache",
		ContextUsage: func(usage hyprovider.ContextUsage) {
			reportedContext = ContextUsage{UsedTokens: usage.UsedTokens, MaxTokens: usage.MaxTokens}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var done hyprovider.Event
	for {
		event, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			t.Fatal(recvErr)
		}
		if event.Kind == hyprovider.EventDone {
			done = event
		}
	}
	if done.Usage.InputTokens != 0 || done.Usage.OutputTokens != 7 || done.Usage.TotalTokens != 7 {
		t.Fatalf("Cursor usage = %+v", done.Usage)
	}
	if reportedContext != (ContextUsage{UsedTokens: 123, MaxTokens: 1000}) {
		t.Fatalf("Cursor context usage = %+v", reportedContext)
	}

	followup, err := buildRunRequest(hyprovider.Request{
		Model: "composer-2",
		Messages: []message.Message{
			message.NewText(message.RoleSystem, "rules"),
			message.NewText(message.RoleUser, "ask"),
			message.NewText(message.RoleAssistant, "answer"),
			message.NewText(message.RoleUser, "follow up"),
		},
	}, "session-cache", "account-1", cache)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := decodeFields(followup.Body)
	if err != nil {
		t.Fatal(err)
	}
	run, err := decodeFields(fieldBytes(envelope, fieldAgentRunRequest))
	if err != nil {
		t.Fatal(err)
	}
	state, err := decodeFields(fieldBytes(run, fieldRunState))
	if err != nil {
		t.Fatal(err)
	}
	if !hasField(state, fieldStateTodos) {
		t.Fatalf("stream checkpoint was not reused: %v", state)
	}
}

func TestFetchUsableModelsRejectsEmptyCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	models, err := FetchUsableModels(context.Background(), CatalogConfig{BaseURL: server.URL, AccessToken: "access", Client: server.Client()})
	if err == nil || len(models) != 0 || !strings.Contains(err.Error(), "no usable models") {
		t.Fatalf("empty catalog models=%+v err=%v", models, err)
	}
}

func TestDecodeNativeToolsMapToAzemTools(t *testing.T) {
	readArgs := encodeString(nil, fieldReadPath, "README.md")
	readArgs = encodeUint32(readArgs, fieldReadOffset, 2)
	readArgs = encodeUint32(readArgs, fieldReadLimit, 3)
	read, ok := decodeStartedTool(startedTool("read-1", fieldToolReadCall, readArgs))
	if !ok || read.Name != azemRead || !bytes.Contains(read.Arguments, []byte(`"path":"README.md"`)) || !bytes.Contains(read.Arguments, []byte(`"startLine":2`)) || !bytes.Contains(read.Arguments, []byte(`"endLine":4`)) {
		t.Fatalf("read = %+v ok=%v", read, ok)
	}

	shellArgs := encodeString(nil, fieldShellCommand, "go test ./internal/provider/cursor")
	shellArgs = encodeUint32(shellArgs, fieldShellTimeout, 30000)
	shell, ok := decodeStartedTool(startedTool("sh-1", fieldToolShellCall, shellArgs))
	if !ok || shell.Name != azemShell || !bytes.Contains(shell.Arguments, []byte(`"command":"go test ./internal/provider/cursor"`)) || !bytes.Contains(shell.Arguments, []byte(`"wall_clock_seconds":30`)) {
		t.Fatalf("shell = %+v ok=%v", shell, ok)
	}

	grepArgs := encodeString(nil, fieldGrepPattern, "decodeStartedTool")
	grepArgs = encodeString(grepArgs, fieldGrepGlob, "internal/**/*.go")
	grepArgs = encodeUint32(grepArgs, fieldGrepHeadLimit, 20)
	grep, ok := decodeStartedTool(startedTool("grep-1", fieldToolGrepCall, grepArgs))
	if !ok || grep.Name != azemSearch || !bytes.Contains(grep.Arguments, []byte(`"query":"decodeStartedTool"`)) || !bytes.Contains(grep.Arguments, []byte(`"regexp":true`)) {
		t.Fatalf("grep = %+v ok=%v", grep, ok)
	}

	writeArgs := encodeString(nil, fieldWritePath, "new.txt")
	writeArgs = encodeString(writeArgs, fieldWriteText, "hello")
	writeArgs = encodeString(writeArgs, fieldWriteToolCallID, "write-1")
	write, ok := decodeExecTool(execTool(fieldExecWriteArgs, writeArgs))
	if !ok || write.Name != azemWrite || write.ID != "write-1" || !bytes.Contains(write.Arguments, []byte(`"content":"hello"`)) {
		t.Fatalf("write = %+v ok=%v", write, ok)
	}

	if _, ok := decodeStartedTool(startedTool("del-1", fieldToolDeleteCall, encodeString(nil, fieldReadPath, "gone.txt"))); ok {
		t.Fatal("delete must be rejected")
	}
	if _, ok := decodeStartedTool(startedTool("edit-1", fieldToolEditCall, encodeString(nil, fieldReadPath, "old.txt"))); ok {
		t.Fatal("edit must be rejected")
	}
	del, ok := decodeExecTool(execTool(fieldExecDeleteArgs, encodeString(nil, fieldDeletePath, "gone.txt")))
	if !ok || del.Name != azemDelete || !bytes.Contains(del.Arguments, []byte(`"path":"gone.txt"`)) {
		t.Fatalf("delete = %+v ok=%v", del, ok)
	}
}

func TestMapExecKindMapsPiFrames(t *testing.T) {
	read, ok := mapExecKind(execEnvelope{ExecID: "pi-read", Kind: fieldExecPiReadArgs, Args: encodeString(nil, fieldReadPath, "x.go")})
	if !ok || read.Name != azemRead || !bytes.Contains(read.Arguments, []byte(`"path":"x.go"`)) {
		t.Fatalf("pi_read = %+v ok=%v", read, ok)
	}
	shell, ok := mapExecKind(execEnvelope{ExecID: "pi-bash", Kind: fieldExecPiBashArgs, Args: encodeString(nil, fieldShellCommand, "pwd")})
	if !ok || shell.Name != azemShell {
		t.Fatalf("pi_bash = %+v ok=%v", shell, ok)
	}
	del, ok := mapExecKind(execEnvelope{ExecID: "del-1", Kind: fieldExecDeleteArgs, Args: encodeString(nil, fieldDeletePath, "gone.txt")})
	if !ok || del.Name != azemDelete {
		t.Fatalf("delete = %+v ok=%v", del, ok)
	}
	editArgs := encodeString(nil, fieldPiEditPath, "a.go")
	editArgs = encodeMessage(editArgs, fieldPiEditEdits, encodeString(encodeString(nil, fieldPiEditOldText, "old"), fieldPiEditNewText, "new"))
	edit, ok := mapExecKind(execEnvelope{ExecID: "edit-1", Kind: fieldExecPiEditArgs, Args: editArgs})
	if !ok || edit.Name != ReplaceTool || !bytes.Contains(edit.Arguments, []byte(`"old_text":"old"`)) {
		t.Fatalf("pi_edit = %+v ok=%v", edit, ok)
	}
	findArgs := encodeString(nil, fieldPiFindPattern, "*.go")
	findArgs = encodeString(findArgs, fieldPiFindPath, "internal")
	find, ok := mapExecKind(execEnvelope{ExecID: "find-1", Kind: fieldExecPiFindArgs, Args: findArgs})
	if !ok || find.Name != azemGlob || !bytes.Contains(find.Arguments, []byte(`"pattern":"*.go"`)) || !bytes.Contains(find.Arguments, []byte(`"path":"internal"`)) {
		t.Fatalf("pi_find = %+v ok=%v", find, ok)
	}
}

func TestSendShellStreamClosesOnce(t *testing.T) {
	var outbound bytes.Buffer
	env := execEnvelope{ID: 9, ExecID: "sh-1", Kind: fieldExecShellStream}
	if err := sendShellStream(&outbound, env, "ok\n", 0); err != nil {
		t.Fatal(err)
	}
	frames, err := decodeConnectFrames(outbound.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	closes := 0
	for _, frame := range frames {
		fields, decErr := decodeFields(frame)
		if decErr != nil {
			t.Fatal(decErr)
		}
		if control := fieldBytes(fields, fieldAgentExecClientControl); len(control) > 0 {
			inner, innerErr := decodeFields(control)
			if innerErr != nil {
				t.Fatal(innerErr)
			}
			if len(fieldBytes(inner, fieldExecControlClose)) > 0 || hasField(inner, fieldExecControlClose) {
				closes++
			}
		}
	}
	if closes != 1 {
		t.Fatalf("stream_close count = %d frames=%d", closes, len(frames))
	}
}

func TestHandleExecReadUsesHostAndDoesNotEmitVenatToolCall(t *testing.T) {
	host := &recordingExecHost{result: HostResult{Content: "file body"}}
	var outbound bytes.Buffer
	args := encodeString(nil, fieldReadPath, "note.txt")
	args = encodeString(args, fieldReadToolCallID, "read-live")
	payload := execTool(fieldExecReadArgs, args)
	var resolvedCall message.ToolCall
	var resolvedResult HostResult
	if err := handleExecServerMessage(context.Background(), &outbound, host, nil, payload, func(call message.ToolCall, result HostResult) {
		resolvedCall, resolvedResult = call, result
	}); err != nil {
		t.Fatal(err)
	}
	if len(host.calls) != 1 || host.calls[0].Name != azemRead || host.calls[0].ID != "read-live" {
		t.Fatalf("host calls = %+v", host.calls)
	}
	if resolvedCall.ID != "read-live" || resolvedResult.Content != "file body" || resolvedResult.IsError {
		t.Fatalf("resolved native exec call=%+v result=%+v", resolvedCall, resolvedResult)
	}
	frames, err := decodeConnectFrames(outbound.Bytes())
	if err != nil || len(frames) < 2 {
		t.Fatalf("frames=%d err=%v", len(frames), err)
	}
	client, err := decodeFields(frames[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(fieldBytes(client, fieldAgentExecClient)) == 0 {
		t.Fatalf("missing exec client result: %v", client)
	}
	control, err := decodeFields(frames[1])
	if err != nil {
		t.Fatal(err)
	}
	if len(fieldBytes(control, fieldAgentExecClientControl)) == 0 {
		t.Fatal("missing stream_close")
	}
}

func TestHandleExecDeleteWritesTypedResult(t *testing.T) {
	host := &recordingExecHost{result: HostResult{Content: `{"path":"gone.txt","size":4}`}}
	var outbound bytes.Buffer
	if err := handleExecServerMessage(context.Background(), &outbound, host, nil, execTool(fieldExecDeleteArgs, encodeString(nil, fieldDeletePath, "gone.txt")), nil); err != nil {
		t.Fatal(err)
	}
	if len(host.calls) != 1 || host.calls[0].Name != azemDelete {
		t.Fatalf("host calls = %+v", host.calls)
	}
	if !bytes.Contains(outbound.Bytes(), []byte("gone.txt")) {
		t.Fatalf("missing delete path in result: %q", outbound.Bytes())
	}
}

func TestHandleExecDeleteNotFound(t *testing.T) {
	host := &recordingExecHost{result: HostResult{Content: "File not found: gone.txt", IsError: true, Code: DeleteCodeNotFound}}
	var outbound bytes.Buffer
	if err := handleExecServerMessage(context.Background(), &outbound, host, nil, execTool(fieldExecDeleteArgs, encodeString(nil, fieldDeletePath, "gone.txt")), nil); err != nil {
		t.Fatal(err)
	}
	frames, err := decodeConnectFrames(outbound.Bytes())
	if err != nil || len(frames) == 0 {
		t.Fatalf("frames=%d err=%v", len(frames), err)
	}
	client, err := decodeFields(frames[0])
	if err != nil {
		t.Fatal(err)
	}
	result := fieldBytes(client, fieldAgentExecClient)
	inner, err := decodeFields(result)
	if err != nil {
		t.Fatal(err)
	}
	deleteResult := fieldBytes(inner, fieldExecClientDelete)
	fields, err := decodeFields(deleteResult)
	if err != nil {
		t.Fatal(err)
	}
	if !hasField(fields, fieldDeleteResultNotFound) {
		t.Fatalf("expected file_not_found: %v", fields)
	}
}

func TestHandleExecUnsupportedThrows(t *testing.T) {
	var outbound bytes.Buffer
	if err := handleExecServerMessage(context.Background(), &outbound, nil, nil, execTool(fieldExecFetchArgs, encodeString(nil, fieldReadPath, "https://example.com")), nil); err != nil {
		t.Fatal(err)
	}
	frames, err := decodeConnectFrames(outbound.Bytes())
	if err != nil || len(frames) != 1 {
		t.Fatalf("frames=%d err=%v", len(frames), err)
	}
	fields, err := decodeFields(frames[0])
	if err != nil {
		t.Fatal(err)
	}
	control := fieldBytes(fields, fieldAgentExecClientControl)
	if len(control) == 0 {
		t.Fatal("expected exec throw")
	}
	inner, err := decodeFields(control)
	if err != nil {
		t.Fatal(err)
	}
	throw := fieldBytes(inner, fieldExecControlThrow)
	if !bytes.Contains(throw, []byte("Unsupported")) {
		t.Fatalf("throw = %q", throw)
	}
}

func TestHandleExecRequestContextAdvertisesTools(t *testing.T) {
	var outbound bytes.Buffer
	if err := handleExecServerMessage(context.Background(), &outbound, nil, []message.ToolDefinition{{Name: "coding.read_file", Description: "read"}}, execTool(fieldExecRequestContext, nil), nil); err != nil {
		t.Fatal(err)
	}
	frames, err := decodeConnectFrames(outbound.Bytes())
	if err != nil || len(frames) == 0 {
		t.Fatalf("frames=%d err=%v", len(frames), err)
	}
	if !bytes.Contains(outbound.Bytes(), []byte("coding.read_file")) {
		t.Fatalf("request_context missing tool: %q", outbound.Bytes())
	}
}

func TestStreamDoesNotEmitVenatToolCallsForNativeExec(t *testing.T) {
	args := encodeString(nil, fieldReadPath, "note.txt")
	exec := encodeConnectFrame(encodeMessage(nil, fieldAgentExecServer, execTool(fieldExecReadArgs, args)), false)
	ended := encodeConnectFrame(encodeMessage(nil, fieldAgentInteraction, encodeBytes(nil, fieldTurnEnded, nil)), false)
	calls := collectToolCalls(t, append(exec, ended...))
	if len(calls) != 0 {
		t.Fatalf("native exec must not become Venat tool calls: %+v", calls)
	}
}

type recordingExecHost struct {
	calls  []message.ToolCall
	result HostResult
}

func (h *recordingExecHost) Execute(_ context.Context, call message.ToolCall) (HostResult, error) {
	h.calls = append(h.calls, call)
	return h.result, nil
}

func startedTool(id string, kind int, args []byte) []byte {
	body := encodeMessage(nil, kind, encodeMessage(nil, fieldNestedArgs, args))
	started := encodeString(nil, fieldToolCallID, id)
	return encodeMessage(started, fieldToolCallBody, body)
}

func execTool(kind int, args []byte) []byte {
	body := encodeUint32(nil, fieldExecMessageID, 7)
	body = encodeKey(body, kind, 2)
	body = encodeVarint(body, uint64(len(args)))
	return append(body, args...)
}

func collectToolCalls(t *testing.T, body []byte) []message.ToolCall {
	t.Helper()
	driver, err := New(func(context.Context) (string, error) { return "access", nil }, "account-1", []string{"composer-2"}, false, NewConversationCache())
	if err != nil {
		t.Fatal(err)
	}
	driver.SetClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/connect+proto"}}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})})
	stream, err := driver.Stream(context.Background(), hyprovider.Request{Model: "composer-2", Messages: []message.Message{message.NewText(message.RoleUser, "hi")}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var calls []message.ToolCall
	for {
		event, recvErr := stream.Recv()
		if recvErr == io.EOF {
			return calls
		}
		if recvErr != nil {
			t.Fatal(recvErr)
		}
		if event.Kind == hyprovider.EventToolCall && event.ToolCall != nil {
			calls = append(calls, *event.ToolCall)
		}
	}
}
