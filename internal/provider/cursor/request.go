package cursor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/azem/internal/provider/responses"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
)

const (
	maxCursorBlobBytes         = 8 << 20
	maxConversationBlobBytes   = 64 << 20
	maxConversationBlobEntries = 4096
)

var kimiK3ModelPattern = regexp.MustCompile(`(?i)(^|/)kimi-k3(?:\.\d+)?(?:[-.:_]|$)`)

type blobStore struct {
	mu         sync.RWMutex
	data       map[string][]byte
	totalBytes int
}

func (s *blobStore) put(data []byte) []byte {
	id := blobID(data)
	s.set(id, data)
	return id
}

func (s *blobStore) set(id, data []byte) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.storeLocked(id, data)
}

func (s *blobStore) setRemote(id, data []byte) error {
	if s == nil {
		return fmt.Errorf("cursor blob store is unavailable")
	}
	if len(id) != 32 || !bytes.Equal(blobID(data), id) {
		return fmt.Errorf("cursor blob digest mismatch")
	}
	if len(data) > maxCursorBlobBytes {
		return fmt.Errorf("cursor blob exceeds %d bytes", maxCursorBlobBytes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := hex.EncodeToString(id)
	if current, ok := s.data[key]; ok {
		if !bytes.Equal(current, data) {
			return fmt.Errorf("cursor blob conflicts with existing digest")
		}
		return nil
	}
	if len(s.data) >= maxConversationBlobEntries {
		return fmt.Errorf("cursor conversation exceeds %d blobs", maxConversationBlobEntries)
	}
	if len(data) > maxConversationBlobBytes-s.totalBytes {
		return fmt.Errorf("cursor conversation blobs exceed %d bytes", maxConversationBlobBytes)
	}
	s.storeLocked(id, data)
	return nil
}

func (s *blobStore) storeLocked(id, data []byte) {
	if s.data == nil {
		s.data = make(map[string][]byte)
	}
	key := hex.EncodeToString(id)
	s.totalBytes += len(data) - len(s.data[key])
	s.data[key] = append([]byte(nil), data...)
}

func (s *blobStore) get(id []byte) []byte {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]byte(nil), s.data[hex.EncodeToString(id)]...)
}

type conversationEntry struct {
	mu         sync.Mutex
	checkpoint []byte
	blobs      *blobStore
}

func (e *conversationEntry) saveCheckpoint(checkpoint []byte) error {
	if e == nil {
		return fmt.Errorf("cursor conversation entry is unavailable")
	}
	if err := validateConversationCheckpoint(checkpoint); err != nil {
		return err
	}
	e.mu.Lock()
	e.checkpoint = append(e.checkpoint[:0], checkpoint...)
	e.mu.Unlock()
	return nil
}

func validateConversationCheckpoint(checkpoint []byte) error {
	if len(checkpoint) == 0 {
		return fmt.Errorf("cursor conversation checkpoint is empty")
	}
	fields, err := decodeFields(checkpoint)
	if err != nil {
		return fmt.Errorf("decode cursor conversation checkpoint: %w", err)
	}
	if raw := fieldBytes(fields, fieldStateTokenDetails); len(raw) > 0 {
		if _, err := decodeFields(raw); err != nil {
			return fmt.Errorf("decode cursor checkpoint token details: %w", err)
		}
	}
	return nil
}

// ConversationCache retains checkpoint and content-addressed blob state across
// short-lived Cursor driver instances within one ProviderRuntime.
type ConversationCache struct {
	mu        sync.Mutex
	entries   map[string]*conversationEntry
	rotations map[string]string
}

// NewConversationCache creates an empty runtime-owned Cursor conversation cache.
func NewConversationCache() *ConversationCache {
	return &ConversationCache{entries: make(map[string]*conversationEntry), rotations: make(map[string]string)}
}

func (c *ConversationCache) resolve(cacheKey, baseConversationID string) (string, *conversationEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	wireID := c.rotations[cacheKey]
	if wireID == "" {
		wireID = baseConversationID
	}
	entry := c.entries[cacheKey]
	if entry == nil {
		entry = &conversationEntry{blobs: &blobStore{data: make(map[string][]byte)}}
		c.entries[cacheKey] = entry
	}
	return wireID, entry
}

func (c *ConversationCache) rotate(cacheKey, currentWireID string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if currentWireID == "" || c.rotations[cacheKey] != "" {
		return false
	}
	c.rotations[cacheKey] = randomID()
	return true
}

// Clear releases every cached conversation after runtime shutdown.
func (c *ConversationCache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.entries = make(map[string]*conversationEntry)
	c.rotations = make(map[string]string)
	c.mu.Unlock()
}

type runPayload struct {
	Body               []byte
	Blobs              *blobStore
	Conversation       *conversationEntry
	Resume             bool
	ConversationID     string
	BaseConversationID string
	CacheKey           string
}

type promptLayout struct {
	rootIDs          [][]byte
	leadingSystemIDs [][]byte
	userText         string
	userImages       []responses.ImageAttachment
	resume           bool
	activeUser       int
}

func buildRunRequest(request hyprovider.Request, conversationID, accountScope string, conversations *ConversationCache) (runPayload, error) {
	return buildRunRequestContext(context.Background(), request, conversationID, accountScope, conversations)
}

func buildRunRequestContext(ctx context.Context, request hyprovider.Request, conversationID, accountScope string, conversations *ConversationCache) (runPayload, error) {
	if conversations == nil {
		return runPayload{}, fmt.Errorf("cursor conversation cache is required")
	}
	accountScope = strings.TrimSpace(accountScope)
	if accountScope == "" {
		return runPayload{}, fmt.Errorf("cursor account scope is required")
	}
	baseConversationID := requestConversationID(request, conversationID)
	cacheKey := conversationCacheKey(accountScope, baseConversationID)
	conversationID, entry := conversations.resolve(cacheKey, baseConversationID)
	entry.mu.Lock()
	defer entry.mu.Unlock()

	layout, err := promptBlobs(ctx, request, entry.blobs)
	if err != nil {
		return runPayload{}, err
	}
	turns, err := buildConversationTurns(request.Messages, layout.activeUser, request.Model, requestAttachmentRoot(ctx), entry.blobs)
	if err != nil {
		return runPayload{}, err
	}
	state, err := buildConversationState(entry.checkpoint, layout.rootIDs, layout.leadingSystemIDs, turns)
	if err != nil {
		return runPayload{}, err
	}
	entry.checkpoint = append(entry.checkpoint[:0], state...)

	var action []byte
	if layout.resume {
		action = encodeBytes(nil, fieldActionResume, nil)
	} else {
		user := encodeCursorUserMessage(layout.userText, randomID(), layout.userImages)
		action = encodeMessage(nil, fieldActionUserMessage, encodeMessage(nil, fieldUserMessageActionMsg, user))
	}
	details := encodeString(nil, fieldModelID, request.Model)
	details = encodeString(details, fieldModelDisplayID, request.Model)
	details = encodeString(details, fieldModelDisplayName, request.Model)
	requested := encodeString(nil, fieldRequestedModelID, request.Model)
	if cursorMaxMode(request) {
		details = encodeUint32(details, fieldModelMaxMode, 1)
		requested = encodeUint32(requested, fieldRequestedMaxMode, 1)
	}
	body := encodeMessage(nil, fieldRunState, state)
	body = encodeMessage(body, fieldRunAction, action)
	body = encodeMessage(body, fieldRunModelDetails, details)
	body = encodeString(body, fieldRunConversationID, conversationID)
	body = encodeMessage(body, fieldRunRequestedModel, requested)
	return runPayload{
		Body: encodeMessage(nil, fieldAgentRunRequest, body), Blobs: entry.blobs, Conversation: entry,
		Resume: layout.resume, ConversationID: conversationID, BaseConversationID: baseConversationID, CacheKey: cacheKey,
	}, nil
}

func cursorMaxMode(request hyprovider.Request) bool {
	if request.ExtraBody == nil {
		return false
	}
	enabled, _ := request.ExtraBody[cursorMaxModeExtraKey].(bool)
	return enabled
}

func conversationCacheKey(accountScope, conversationID string) string {
	return hex.EncodeToString(blobID([]byte(strings.TrimSpace(accountScope) + "\x00" + conversationID)))
}

func requestConversationID(request hyprovider.Request, explicit string) string {
	if value := strings.TrimSpace(explicit); value != "" {
		return value
	}
	if value := strings.TrimSpace(request.PromptCacheKey); value != "" {
		return value
	}
	if value := strings.TrimSpace(request.Metadata["session_id"]); value != "" {
		return value
	}
	return randomID()
}

func requestAttachmentRoot(ctx context.Context) string {
	return responses.AttachmentRootFromContext(ctx)
}

func promptBlobs(ctx context.Context, request hyprovider.Request, blobs *blobStore) (promptLayout, error) {
	layout := promptLayout{activeUser: activeUserIndex(request.Messages)}
	attachmentRoot := requestAttachmentRoot(ctx)
	if layout.activeUser >= 0 {
		layout.userText = strings.TrimSpace(request.Messages[layout.activeUser].Text)
		var err error
		layout.userImages, err = responses.LoadImageAttachments(request.Messages[layout.activeUser].Metadata, attachmentRoot)
		if err != nil {
			return promptLayout{}, err
		}
	}
	layout.resume = layout.activeUser < 0 || (layout.userText == "" && len(layout.userImages) == 0)

	type entry struct {
		id   []byte
		role message.Role
	}
	entries := make([]entry, 0, len(request.Messages)+1)
	for _, current := range cursorRootMessages(request.Messages, layout.activeUser) {
		encoded, include, err := encodeRootMessage(current, request.Model, attachmentRoot)
		if err != nil {
			return promptLayout{}, err
		}
		if !include {
			continue
		}
		entries = append(entries, entry{id: blobs.put(encoded), role: current.Role})
		if current.Role == message.RoleAssistant {
			nativeExecs, err := decodeCursorNativeExecs(current.ProviderState)
			if err != nil {
				return promptLayout{}, err
			}
			for _, record := range nativeExecs {
				toolMessage := message.NewToolResult(message.ToolResult{
					ToolCallID: record.CallID, Name: record.Name, Content: record.Content, IsError: record.IsError,
				})
				encodedResult, _, err := encodeRootMessage(toolMessage, request.Model, attachmentRoot)
				if err != nil {
					return promptLayout{}, err
				}
				entries = append(entries, entry{id: blobs.put(encodedResult), role: message.RoleTool})
			}
		}
	}
	if len(entries) == 0 || entries[0].role != message.RoleSystem {
		entries = append([]entry{{
			id: blobs.put([]byte(`{"role":"system","content":"You are a helpful assistant."}`)), role: message.RoleSystem,
		}}, entries...)
	}
	layout.rootIDs = make([][]byte, 0, len(entries))
	for _, current := range entries {
		layout.rootIDs = append(layout.rootIDs, current.id)
		if len(layout.leadingSystemIDs) == len(layout.rootIDs)-1 && current.role == message.RoleSystem {
			layout.leadingSystemIDs = append(layout.leadingSystemIDs, current.id)
		}
	}
	return layout, nil
}

func cursorRootMessages(messages []message.Message, activeUser int) []message.Message {
	ordered := make([]message.Message, 0, len(messages))
	for index := 0; index < len(messages); {
		current := messages[index]
		if !isSharedUser(current) {
			if index != activeUser {
				ordered = append(ordered, current)
			}
			index++
			continue
		}
		tailEnd := index + 1
		for tailEnd < len(messages) && agentruntime.MessageVisibilityOf(messages[tailEnd]) == agentruntime.MessageVisibilityPrivate {
			tailEnd++
		}
		ordered = append(ordered, messages[index+1:tailEnd]...)
		if index != activeUser {
			ordered = append(ordered, current)
		}
		index = tailEnd
	}
	return ordered
}

func isSharedUser(current message.Message) bool {
	return agentruntime.MessageVisibilityOf(current) != agentruntime.MessageVisibilityPrivate &&
		(current.Role == message.RoleUser || current.Role == message.RoleCustom)
}

func activeUserIndex(messages []message.Message) int {
	for index := len(messages) - 1; index >= 0; index-- {
		current := messages[index]
		if agentruntime.MessageVisibilityOf(current) == agentruntime.MessageVisibilityPrivate {
			continue
		}
		if isSharedUser(current) {
			return index
		}
		return -1
	}
	return -1
}

type cursorTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type cursorReasoningProvider struct {
	Cursor struct {
		ModelName string `json:"modelName"`
	} `json:"cursor"`
}

type cursorReasoningPart struct {
	Type            string                  `json:"type"`
	Text            string                  `json:"text"`
	ProviderOptions cursorReasoningProvider `json:"providerOptions"`
	Signature       string                  `json:"signature,omitempty"`
}

type cursorToolCallPart struct {
	Type       string         `json:"type"`
	ToolCallID string         `json:"toolCallId"`
	ToolName   string         `json:"toolName"`
	Args       map[string]any `json:"args"`
}

type cursorUserRoot struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

type cursorImagePart struct {
	Type      string `json:"type"`
	Image     string `json:"image"`
	MediaType string `json:"mediaType"`
}

type cursorAssistantRoot struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

type cursorToolResultPart struct {
	Type       string `json:"type"`
	ToolName   string `json:"toolName"`
	ToolCallID string `json:"toolCallId"`
	Result     string `json:"result"`
	IsError    bool   `json:"isError,omitempty"`
}

type cursorToolRoot struct {
	Role    string                 `json:"role"`
	ID      string                 `json:"id"`
	Content []cursorToolResultPart `json:"content"`
}

type cursorProviderStateV1 struct {
	Version     int                      `json:"version"`
	Provider    string                   `json:"provider"`
	Model       string                   `json:"model"`
	NativeExecs []cursorNativeExecRecord `json:"native_execs,omitempty"`
}

type cursorNativeExecRecord struct {
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Code      string          `json:"code,omitempty"`
}

func decodeCursorProviderState(state json.RawMessage) (cursorProviderStateV1, bool, error) {
	if len(state) == 0 {
		return cursorProviderStateV1{}, false, nil
	}
	var decoded cursorProviderStateV1
	if err := json.Unmarshal(state, &decoded); err != nil {
		return cursorProviderStateV1{}, false, fmt.Errorf("decode Cursor provider state: %w", err)
	}
	if decoded.Version != 1 {
		return cursorProviderStateV1{}, false, fmt.Errorf("unsupported Cursor provider state version %d", decoded.Version)
	}
	if decoded.Provider != "cursor" || strings.TrimSpace(decoded.Model) == "" {
		return cursorProviderStateV1{}, false, fmt.Errorf("Cursor provider state is missing its source identity")
	}
	for _, record := range decoded.NativeExecs {
		if strings.TrimSpace(record.CallID) == "" || strings.TrimSpace(record.Name) == "" {
			return cursorProviderStateV1{}, false, fmt.Errorf("Cursor provider state contains an incomplete native exec")
		}
	}
	return decoded, true, nil
}

func decodeCursorNativeExecs(state json.RawMessage) ([]cursorNativeExecRecord, error) {
	decoded, ok, err := decodeCursorProviderState(state)
	if err != nil || !ok {
		return nil, err
	}
	return decoded.NativeExecs, nil
}

func canReplayCursorThinking(current message.Message, modelID string) (bool, error) {
	if current.Thinking == "" || !kimiK3ModelPattern.MatchString(modelID) {
		return false, nil
	}
	state, ok, err := decodeCursorProviderState(current.ProviderState)
	if err != nil || !ok {
		return false, err
	}
	return state.Provider == "cursor" && state.Model == modelID, nil
}

func encodeCursorUserMessage(text, id string, images []responses.ImageAttachment) []byte {
	user := encodeString(nil, fieldUserMessageText, text)
	user = encodeString(user, fieldUserMessageID, id)
	if len(images) == 0 {
		return user
	}
	var selected []byte
	for _, image := range images {
		item := encodeString(nil, fieldSelectedImageUUID, randomID())
		item = encodeString(item, fieldSelectedImageMIME, image.MediaType)
		item = encodeBytes(item, fieldSelectedImageData, image.Data)
		selected = encodeMessage(selected, fieldSelectedContextImages, item)
	}
	return encodeMessage(user, fieldUserMessageSelectedContext, selected)
}

func encodeRootMessage(current message.Message, modelID, attachmentRoot string) ([]byte, bool, error) {
	var value any
	switch current.Role {
	case message.RoleSystem:
		value = struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{Role: "system", Content: current.Text}
	case message.RoleUser, message.RoleCustom:
		content := make([]any, 0, 2)
		if text := strings.TrimSpace(current.Text); text != "" {
			content = append(content, cursorTextPart{Type: "text", Text: text})
		}
		images, err := responses.LoadImageAttachments(current.Metadata, attachmentRoot)
		if err != nil {
			return nil, false, err
		}
		for _, image := range images {
			content = append(content, cursorImagePart{
				Type: "image", Image: "data:" + image.MediaType + ";base64," + base64.StdEncoding.EncodeToString(image.Data),
				MediaType: image.MediaType,
			})
		}
		if len(content) == 0 {
			return nil, false, nil
		}
		value = cursorUserRoot{Role: "user", Content: content}
	case message.RoleAssistant:
		content, err := assistantContent(current, modelID)
		if err != nil {
			return nil, false, err
		}
		if len(content) == 0 {
			return nil, false, nil
		}
		value = cursorAssistantRoot{Role: "assistant", Content: content}
	case message.RoleTool:
		if current.ToolResult == nil {
			return nil, false, nil
		}
		result := current.ToolResult
		value = cursorToolRoot{Role: "tool", ID: result.ToolCallID, Content: []cursorToolResultPart{{
			Type: "tool-result", ToolName: result.Name, ToolCallID: result.ToolCallID, Result: result.Content, IsError: result.IsError,
		}}}
	default:
		return nil, false, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false, fmt.Errorf("encode cursor root message: %w", err)
	}
	return encoded, true, nil
}

func assistantContent(current message.Message, modelID string) ([]any, error) {
	content := make([]any, 0, len(current.ToolCalls)+2)
	replayThinking, err := canReplayCursorThinking(current, modelID)
	if err != nil {
		return nil, err
	}
	if replayThinking {
		part := cursorReasoningPart{Type: "reasoning", Text: current.Thinking, Signature: current.ThinkingSignature}
		part.ProviderOptions.Cursor.ModelName = modelID
		content = append(content, part)
	}
	if current.Text != "" {
		content = append(content, cursorTextPart{Type: "text", Text: current.Text})
	}
	for _, call := range current.ToolCalls {
		args, err := decodeToolArguments(call)
		if err != nil {
			return nil, err
		}
		content = append(content, cursorToolCallPart{Type: "tool-call", ToolCallID: call.ID, ToolName: call.Name, Args: args})
	}
	nativeExecs, err := decodeCursorNativeExecs(current.ProviderState)
	if err != nil {
		return nil, err
	}
	for _, record := range nativeExecs {
		args, err := decodeToolArguments(message.ToolCall{ID: record.CallID, Name: record.Name, Arguments: record.Arguments})
		if err != nil {
			return nil, err
		}
		content = append(content, cursorToolCallPart{Type: "tool-call", ToolCallID: record.CallID, ToolName: record.Name, Args: args})
	}
	return content, nil
}

func decodeToolArguments(call message.ToolCall) (map[string]any, error) {
	if len(call.Arguments) == 0 {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return nil, fmt.Errorf("decode cursor tool arguments for %s: %w", call.Name, err)
	}
	if args == nil {
		return nil, fmt.Errorf("cursor tool arguments for %s must be an object", call.Name)
	}
	return args, nil
}

func encodeMCPToolDef(tool message.ToolDefinition) []byte {
	if tool.Name == "" {
		return nil
	}
	schemaJSON, err := json.Marshal(tool.InputSchema)
	var schema any
	if err != nil || json.Unmarshal(schemaJSON, &schema) != nil {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	encodedSchema, err := encodeProtoValue(schema)
	if err != nil {
		encodedSchema, _ = encodeProtoValue(map[string]any{"type": "object", "properties": map[string]any{}})
	}
	item := encodeString(nil, fieldMCPDefName, tool.Name)
	item = encodeString(item, fieldMCPDefDescription, tool.Description)
	item = encodeString(item, fieldMCPDefProvider, "azem")
	item = encodeString(item, fieldMCPDefToolName, tool.Name)
	return encodeBytes(item, fieldMCPDefSchema, encodedSchema)
}

func buildConversationTurns(messages []message.Message, activeUser int, modelID, attachmentRoot string, blobs *blobStore) ([][]byte, error) {
	historyEnd := len(messages)
	if activeUser >= 0 {
		historyEnd = activeUser
	}
	results := make(map[string]*message.ToolResult)
	paired := make(map[string]bool)
	for index := range messages[:historyEnd] {
		current := messages[index]
		if current.Role == message.RoleTool && current.ToolResult != nil {
			result := *current.ToolResult
			results[result.ToolCallID] = &result
		}
	}
	for index := range messages[:historyEnd] {
		for _, call := range messages[index].ToolCalls {
			if results[call.ID] != nil {
				paired[call.ID] = true
			}
		}
	}

	turns := make([][]byte, 0)
	for index := 0; index < historyEnd; {
		current := messages[index]
		if !isSharedUser(current) {
			index++
			continue
		}
		userText := strings.TrimSpace(current.Text)
		images, err := responses.LoadImageAttachments(current.Metadata, attachmentRoot)
		if err != nil {
			return nil, err
		}
		if userText == "" && len(images) == 0 {
			index++
			continue
		}
		seed := fmt.Sprintf("u:%d:%s", len(turns), userText)
		for _, image := range images {
			seed += ":" + hex.EncodeToString(blobID(image.Data))
		}
		userID := blobs.put(encodeCursorUserMessage(userText, deterministicID(seed), images))
		steps := make([][]byte, 0)
		index++
		for index < historyEnd && !isSharedUser(messages[index]) {
			stepMessage := messages[index]
			switch stepMessage.Role {
			case message.RoleAssistant:
				replayThinking, err := canReplayCursorThinking(stepMessage, modelID)
				if err != nil {
					return nil, err
				}
				if replayThinking {
					thinking := encodeString(nil, fieldThinkingMessageText, stepMessage.Thinking)
					steps = append(steps, blobs.put(encodeMessage(nil, fieldConversationStepThinking, thinking)))
				}
				if stepMessage.Text != "" {
					assistant := encodeString(nil, fieldAssistantMessageText, stepMessage.Text)
					steps = append(steps, blobs.put(encodeMessage(nil, fieldConversationStepAssistant, assistant)))
				}
				for _, call := range stepMessage.ToolCalls {
					step, err := encodeConversationToolStep(call, results[call.ID])
					if err != nil {
						return nil, err
					}
					steps = append(steps, blobs.put(step))
				}
				nativeExecs, err := decodeCursorNativeExecs(stepMessage.ProviderState)
				if err != nil {
					return nil, err
				}
				for _, record := range nativeExecs {
					result := &message.ToolResult{
						ToolCallID: record.CallID, Name: record.Name, Content: record.Content, IsError: record.IsError,
					}
					step, err := encodeConversationToolStep(message.ToolCall{
						ID: record.CallID, Name: record.Name, Arguments: record.Arguments,
					}, result)
					if err != nil {
						return nil, err
					}
					steps = append(steps, blobs.put(step))
				}
			case message.RoleTool:
				if stepMessage.ToolResult != nil && !paired[stepMessage.ToolResult.ToolCallID] && stepMessage.ToolResult.Content != "" {
					prefix := "[Tool Result]"
					if stepMessage.ToolResult.IsError {
						prefix = "[Tool Error]"
					}
					assistant := encodeString(nil, fieldAssistantMessageText, prefix+"\n"+stepMessage.ToolResult.Content)
					steps = append(steps, blobs.put(encodeMessage(nil, fieldConversationStepAssistant, assistant)))
				}
			}
			index++
		}
		agentTurn := encodeBytes(nil, fieldAgentTurnUserMessage, userID)
		for _, stepID := range steps {
			agentTurn = encodeBytes(agentTurn, fieldAgentTurnSteps, stepID)
		}
		turn := encodeMessage(nil, fieldConversationTurnAgent, agentTurn)
		turns = append(turns, blobs.put(turn))
	}
	return turns, nil
}

func encodeConversationToolStep(call message.ToolCall, result *message.ToolResult) ([]byte, error) {
	args, err := decodeToolArguments(call)
	if err != nil {
		return nil, err
	}
	mcpArgs := encodeString(nil, fieldMCPArgsName, call.Name)
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value, err := encodeProtoValue(args[key])
		if err != nil {
			return nil, fmt.Errorf("encode cursor tool argument %s.%s: %w", call.Name, key, err)
		}
		entry := encodeBytes(nil, fieldMapKey, []byte(key))
		entry = encodeBytes(entry, fieldMapValue, value)
		mcpArgs = encodeMessage(mcpArgs, fieldMCPArgsMap, entry)
	}
	mcpArgs = encodeString(mcpArgs, fieldMCPArgsToolCallID, call.ID)
	mcpArgs = encodeString(mcpArgs, fieldMCPArgsProvider, "pi-agent")
	mcpArgs = encodeString(mcpArgs, fieldMCPArgsToolName, call.Name)
	mcpCall := encodeMessage(nil, fieldMCPCallArgs, mcpArgs)
	if result != nil {
		mcpCall = encodeMessage(mcpCall, fieldMCPCallResult, encodeConversationToolResult(*result))
	}
	toolCall := encodeMessage(nil, fieldToolCallMCP, mcpCall)
	toolCall = encodeString(toolCall, fieldToolCallEnvelopeID, call.ID)
	return encodeMessage(nil, fieldConversationStepTool, toolCall), nil
}

func encodeConversationToolResult(result message.ToolResult) []byte {
	if result.IsError {
		errorMessage := encodeString(nil, fieldMCPToolErrorText, result.Content)
		return encodeBytes(nil, fieldMCPToolResultError, errorMessage)
	}
	text := encodeString(nil, fieldMCPTextValue, result.Content)
	item := encodeBytes(nil, fieldMCPContentText, text)
	success := encodeBytes(nil, fieldMCPSuccessContent, item)
	return encodeBytes(nil, fieldMCPToolResultSuccess, success)
}

func encodeProtoValue(value any) ([]byte, error) {
	switch current := value.(type) {
	case nil:
		return encodeUint32(nil, fieldValueNull, 0), nil
	case bool:
		encoded := encodeKey(nil, fieldValueBool, 0)
		if current {
			return encodeVarint(encoded, 1), nil
		}
		return encodeVarint(encoded, 0), nil
	case float64:
		if math.IsNaN(current) || math.IsInf(current, 0) {
			return nil, fmt.Errorf("number is not finite")
		}
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, math.Float64bits(current))
		return append(encodeKey(nil, fieldValueNumber, 1), buf...), nil
	case string:
		return encodeBytes(nil, fieldValueString, []byte(current)), nil
	case []any:
		var list []byte
		for _, item := range current {
			encoded, err := encodeProtoValue(item)
			if err != nil {
				return nil, err
			}
			list = encodeMessage(list, fieldListValueItem, encoded)
		}
		return encodeBytes(nil, fieldValueList, list), nil
	case map[string]any:
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var object []byte
		for _, key := range keys {
			encoded, err := encodeProtoValue(current[key])
			if err != nil {
				return nil, err
			}
			entry := encodeBytes(nil, fieldMapKey, []byte(key))
			entry = encodeBytes(entry, fieldMapValue, encoded)
			object = encodeMessage(object, fieldStructFields, entry)
		}
		return encodeBytes(nil, fieldValueStruct, object), nil
	default:
		return nil, fmt.Errorf("unsupported JSON value %T", value)
	}
}

func decodeProtoValue(payload []byte) (any, error) {
	return decodeProtoValueWithBudget(payload, &protoDecodeBudget{}, 0)
}

func decodeProtoValueWithBudget(payload []byte, budget *protoDecodeBudget, depth int) (any, error) {
	if depth > maxCursorProtoValueDepth {
		return nil, fmt.Errorf("cursor protobuf Value nesting exceeds %d levels", maxCursorProtoValueDepth)
	}
	fields, err := decodeFieldsWithBudget(payload, budget)
	if err != nil {
		return nil, err
	}
	for _, field := range fields {
		switch {
		case field.Field == fieldValueNull && field.Wire == 0:
			return nil, nil
		case field.Field == fieldValueNumber && field.Wire == 1:
			if len(field.Bytes) != 8 {
				return nil, fmt.Errorf("cursor protobuf number has %d bytes", len(field.Bytes))
			}
			value := math.Float64frombits(binary.LittleEndian.Uint64(field.Bytes))
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, fmt.Errorf("cursor protobuf number is not finite")
			}
			return value, nil
		case field.Field == fieldValueString && field.Wire == 2:
			return string(field.Bytes), nil
		case field.Field == fieldValueBool && field.Wire == 0:
			return field.Var != 0, nil
		case field.Field == fieldValueStruct && field.Wire == 2:
			objectFields, err := decodeFieldsWithBudget(field.Bytes, budget)
			if err != nil {
				return nil, err
			}
			object := make(map[string]any)
			for _, rawEntry := range fieldAllBytes(objectFields, fieldStructFields) {
				entry, err := decodeFieldsWithBudget(rawEntry, budget)
				if err != nil {
					return nil, err
				}
				if !hasField(entry, fieldMapKey) || !hasField(entry, fieldMapValue) {
					return nil, fmt.Errorf("cursor protobuf struct entry is incomplete")
				}
				value, err := decodeProtoValueWithBudget(fieldBytes(entry, fieldMapValue), budget, depth+1)
				if err != nil {
					return nil, err
				}
				object[fieldString(entry, fieldMapKey)] = value
			}
			return object, nil
		case field.Field == fieldValueList && field.Wire == 2:
			listFields, err := decodeFieldsWithBudget(field.Bytes, budget)
			if err != nil {
				return nil, err
			}
			items := fieldAllBytes(listFields, fieldListValueItem)
			list := make([]any, 0, len(items))
			for _, rawItem := range items {
				item, err := decodeProtoValueWithBudget(rawItem, budget, depth+1)
				if err != nil {
					return nil, err
				}
				list = append(list, item)
			}
			return list, nil
		}
	}
	return nil, fmt.Errorf("cursor protobuf Value omitted its oneof")
}

func buildConversationState(cached []byte, rootIDs, leadingSystemIDs, turns [][]byte) ([]byte, error) {
	var base []protoField
	if len(cached) > 0 {
		fields, err := decodeFields(cached)
		if err != nil {
			return nil, fmt.Errorf("decode cursor conversation checkpoint: %w", err)
		}
		if promptHeadMatches(fieldAllBytes(fields, fieldStateRootPromptJSON), leadingSystemIDs) {
			base = fields
		}
	}
	var state []byte
	for _, id := range rootIDs {
		state = encodeBytes(state, fieldStateRootPromptJSON, id)
	}
	for _, field := range base {
		if field.Field > fieldStateRootPromptJSON && field.Field < fieldStateTurns {
			state = encodeProtoField(state, field)
		}
	}
	for _, id := range turns {
		state = encodeBytes(state, fieldStateTurns, id)
	}
	for _, field := range base {
		if field.Field > fieldStateTurns {
			state = encodeProtoField(state, field)
		}
	}
	return state, nil
}

func promptHeadMatches(cached, current [][]byte) bool {
	if len(cached) < len(current) {
		return false
	}
	for index := range current {
		if !bytes.Equal(cached[index], current[index]) {
			return false
		}
	}
	return true
}

func deterministicID(seed string) string {
	id := blobID([]byte(seed))
	return hex.EncodeToString(id[:16])
}

func randomID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "cursor-conversation"
	}
	return hex.EncodeToString(buf)
}
