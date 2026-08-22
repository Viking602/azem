package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/netproxy"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
)

// ContextUsageReporterExtraKey carries the optional non-billable Cursor
// checkpoint-occupancy callback in provider.Request.ExtraBody.
const ContextUsageReporterExtraKey = "cursor_context_usage_reporter"

const cursorMaxModeExtraKey = "cursor_max_mode"

// ContextUsage reports Cursor's current context occupancy and declared limit.
type ContextUsage struct {
	UsedTokens int
	MaxTokens  int
}

// ContextUsageReporter observes provider checkpoint occupancy.
type ContextUsageReporter func(ContextUsage)

// Driver streams Cursor AgentService turns.
type Driver struct {
	token         func(context.Context) (string, error)
	accountScope  string
	baseURL       string
	models        []string
	cursorMaxMode bool
	client        *http.Client
	conversations *ConversationCache
	retryDelay    func(int) time.Duration
	maxRetryDelay time.Duration
	retryObserver hyprovider.RetryObserver
}

// New constructs a Cursor driver sharing the supplied runtime conversation cache.
func New(token func(context.Context) (string, error), accountScope string, models []string, cursorMaxMode bool, conversations *ConversationCache) (*Driver, error) {
	if token == nil {
		return nil, fmt.Errorf("cursor access token resolver is required")
	}
	accountScope = strings.TrimSpace(accountScope)
	if accountScope == "" {
		return nil, fmt.Errorf("cursor account scope is required")
	}
	if conversations == nil {
		return nil, fmt.Errorf("cursor conversation cache is required")
	}
	return &Driver{
		token: token, accountScope: accountScope, models: append([]string(nil), models...), cursorMaxMode: cursorMaxMode, conversations: conversations,
		baseURL: DefaultAPIURL, client: netproxy.NewHTTPClient(0),
	}, nil
}

func (d *Driver) Metadata() hyprovider.Metadata {
	return hyprovider.Metadata{Name: "cursor-agent", Models: append([]string(nil), d.models...), Version: "3"}
}

func (d *Driver) SetRetryObserver(observer hyprovider.RetryObserver) { d.retryObserver = observer }
func (d *Driver) SetMaxRetryDelay(delay time.Duration)               { d.maxRetryDelay = delay }
func (d *Driver) SetBaseURL(baseURL string) {
	if strings.TrimSpace(baseURL) != "" {
		d.baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	}
}

func (d *Driver) SetClient(client *http.Client) {
	if client != nil {
		d.client = client
	}
}

func (d *Driver) Stream(ctx context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	baseConversationID := requestConversationID(request, "")
	open := func() (hyprovider.Stream, error) { return d.openStream(ctx, request, baseConversationID) }
	return hyprovider.OpenRetryingStream(ctx, open, hyprovider.StreamRetryOptions{
		Delay: d.retryDelay, MaxDelay: d.maxRetryDelay, Observer: d.retryObserver,
	})
}

func (d *Driver) openStream(ctx context.Context, request hyprovider.Request, baseConversationID string) (hyprovider.Stream, error) {
	accessToken, err := d.token(ctx)
	if err != nil {
		return nil, err
	}
	request = withCursorMaxMode(request, d.cursorMaxMode)
	payload, err := buildRunRequest(request, baseConversationID, d.accountScope, d.conversations)
	if err != nil {
		return nil, err
	}
	body, writer := io.Pipe()
	var contextUsageReporter ContextUsageReporter
	var execHost ExecHost
	var todoSync TodoSync
	if request.ExtraBody != nil {
		contextUsageReporter, _ = request.ExtraBody[ContextUsageReporterExtraKey].(ContextUsageReporter)
		execHost, _ = request.ExtraBody[ExecHostExtraKey].(ExecHost)
		todoSync, _ = request.ExtraBody[TodoSyncExtraKey].(TodoSync)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, d.baseURL+runPath, body)
	if err != nil {
		_ = writer.Close()
		return nil, err
	}
	applyCursorHeaders(httpRequest.Header, accessToken, DefaultClientVersion)
	httpRequest.Header.Set("Content-Type", "application/connect+proto")
	httpRequest.Header.Set("Connect-Protocol-Version", "1")
	httpRequest.Header.Set("TE", "trailers")
	go func() {
		if _, writeErr := writer.Write(encodeConnectFrame(payload.Body, false)); writeErr != nil {
			_ = writer.CloseWithError(writeErr)
		}
	}()
	response, err := d.client.Do(httpRequest)
	if err != nil {
		_ = writer.Close()
		return nil, err
	}
	if response.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		_ = response.Body.Close()
		_ = writer.Close()
		return nil, hyprovider.NewHTTPError("cursor", response.StatusCode, strings.TrimSpace(string(raw)))
	}
	streamCtx, cancel := context.WithCancel(ctx)
	events := make(chan hyprovider.Event, 16)
	go func() {
		defer cancel()
		d.read(streamCtx, response, writer, payload.Blobs, payload.Conversation, payload.CacheKey, payload.ConversationID, execHost, todoSync, request.Model, request.Tools, contextUsageReporter, events)
	}()
	return &eventStream{events: events, close: func() {
		cancel()
		_ = writer.Close()
		_ = response.Body.Close()
	}}, nil
}

func withCursorMaxMode(request hyprovider.Request, enabled bool) hyprovider.Request {
	if !enabled {
		return request
	}
	extra := make(map[string]any, len(request.ExtraBody)+1)
	for key, value := range request.ExtraBody {
		extra[key] = value
	}
	extra[cursorMaxModeExtraKey] = true
	request.ExtraBody = extra
	return request
}

type cursorStreamUsage struct {
	outputTokens  int
	contextTokens int
	contextLimit  int
	sawTokenDelta bool
	reportContext ContextUsageReporter
	modelID       string
	nativeExecs   []cursorNativeExecRecord
}

func (u *cursorStreamUsage) observeCheckpoint(checkpoint []byte) error {
	if u == nil {
		return nil
	}
	state, err := decodeFields(checkpoint)
	if err != nil {
		return err
	}
	raw := fieldBytes(state, fieldStateTokenDetails)
	if len(raw) == 0 {
		return nil
	}
	details, err := decodeFields(raw)
	if err != nil {
		return err
	}
	if u.sawTokenDelta {
		return nil
	}
	u.contextTokens = int(fieldUint32(details, fieldTokenUsed))
	u.contextLimit = int(fieldUint32(details, fieldTokenMax))
	if u.contextTokens > 0 && u.reportContext != nil {
		u.reportContext(ContextUsage{UsedTokens: u.contextTokens, MaxTokens: u.contextLimit})
	}
	return nil
}

func (u cursorStreamUsage) providerUsage() hyprovider.Usage {
	return hyprovider.Usage{OutputTokens: u.outputTokens, TotalTokens: u.outputTokens}
}

func (u *cursorStreamUsage) recordNativeExec(call message.ToolCall, result HostResult) {
	if u == nil {
		return
	}
	u.nativeExecs = append(u.nativeExecs, cursorNativeExecRecord{
		CallID: call.ID, Name: call.Name, Arguments: append(json.RawMessage(nil), call.Arguments...),
		Content: result.Content, IsError: result.IsError, Code: result.Code,
	})
}

func (u cursorStreamUsage) providerState() (json.RawMessage, error) {
	if strings.TrimSpace(u.modelID) == "" {
		return nil, fmt.Errorf("encode Cursor provider state: model id is empty")
	}
	encoded, err := json.Marshal(cursorProviderStateV1{
		Version: 1, Provider: "cursor", Model: u.modelID, NativeExecs: u.nativeExecs,
	})
	if err != nil {
		return nil, fmt.Errorf("encode Cursor provider state: %w", err)
	}
	return encoded, nil
}

func sendCursorEvent(ctx context.Context, events chan<- hyprovider.Event, event hyprovider.Event) bool {
	select {
	case events <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

func (d *Driver) read(ctx context.Context, response *http.Response, writer *io.PipeWriter, blobs *blobStore, conversation *conversationEntry, cacheKey, wireConversationID string, execHost ExecHost, todoSync TodoSync, modelID string, tools []message.ToolDefinition, reportContext ContextUsageReporter, events chan hyprovider.Event) {
	defer close(events)
	defer func() { _ = writer.Close() }()
	defer func() { _ = response.Body.Close() }()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeat.C:
				_, _ = writer.Write(encodeConnectFrame(encodeMessage(nil, fieldAgentHeartbeat, nil), false))
			}
		}
	}()
	var pending []byte
	buf := make([]byte, 32<<10)
	var sawTurnEnded bool
	usage := cursorStreamUsage{reportContext: reportContext, modelID: modelID}
	for {
		if ctx.Err() != nil {
			return
		}
		n, err := response.Body.Read(buf)
		if ctx.Err() != nil {
			return
		}
		if n > 0 {
			if len(pending)+n > maxConnectFrameBytes+5 {
				sendCursorEvent(ctx, events, hyprovider.Event{Kind: hyprovider.EventError, Err: fmt.Errorf("cursor connect frame buffer exceeds %d bytes", maxConnectFrameBytes)})
				return
			}
			pending = append(pending, buf[:n]...)
			for {
				frame, rest, ok, frameErr := nextConnectFrame(pending)
				if isCursorResourceExhausted(frameErr) && !usage.sawTokenDelta {
					d.conversations.rotate(cacheKey, wireConversationID)
				}
				if frameErr != nil {
					sendCursorEvent(ctx, events, hyprovider.Event{Kind: hyprovider.EventError, Err: frameErr})
					return
				}
				pending = rest
				if !ok {
					break
				}
				if ctx.Err() != nil {
					return
				}
				if handleErr := d.handleFrame(ctx, frame, writer, blobs, conversation, execHost, todoSync, tools, events, &usage, &sawTurnEnded); handleErr != nil {
					sendCursorEvent(ctx, events, hyprovider.Event{Kind: hyprovider.EventError, Err: handleErr})
					return
				}
			}
		}
		if err == io.EOF {
			if !sawTurnEnded {
				sendCursorEvent(ctx, events, hyprovider.Event{Kind: hyprovider.EventError, Err: fmt.Errorf("cursor stream ended before turnEnded")})
				return
			}
			providerState, stateErr := usage.providerState()
			if stateErr != nil {
				sendCursorEvent(ctx, events, hyprovider.Event{Kind: hyprovider.EventError, Err: stateErr})
				return
			}
			sendCursorEvent(ctx, events, hyprovider.Event{
				Kind: hyprovider.EventDone, StopReason: hyprovider.StopReasonComplete,
				Usage: usage.providerUsage(), ProviderState: providerState,
			})
			return
		}
		if err != nil {
			sendCursorEvent(ctx, events, hyprovider.Event{Kind: hyprovider.EventError, Err: err})
			return
		}
	}
}

func (d *Driver) handleFrame(ctx context.Context, frame []byte, writer *io.PipeWriter, blobs *blobStore, conversation *conversationEntry, execHost ExecHost, todoSync TodoSync, tools []message.ToolDefinition, events chan hyprovider.Event, usage *cursorStreamUsage, sawTurnEnded *bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fields, err := decodeFields(frame)
	if err != nil {
		return err
	}
	if raw := fieldBytes(fields, fieldAgentCheckpoint); len(raw) > 0 {
		if err := usage.observeCheckpoint(raw); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return conversation.saveCheckpoint(raw)
	}
	if raw := fieldBytes(fields, fieldAgentKVServer); len(raw) > 0 {
		return replyBlob(writer, raw, blobs)
	}
	if raw := fieldBytes(fields, fieldAgentExecServer); len(raw) > 0 {
		return handleExecServerMessage(ctx, writer, execHost, tools, raw, usage.recordNativeExec)
	}
	raw := fieldBytes(fields, fieldAgentInteraction)
	if len(raw) == 0 {
		return nil
	}
	update, err := decodeFields(raw)
	if err != nil {
		return err
	}
	if nested := fieldBytes(update, fieldTextDelta); len(nested) > 0 {
		if inner, innerErr := decodeFields(nested); innerErr == nil {
			if delta := fieldString(inner, fieldUpdateText); delta != "" {
				if !sendCursorEvent(ctx, events, hyprovider.Event{Kind: hyprovider.EventTextDelta, Text: delta}) {
					return ctx.Err()
				}
			}
		}
	}
	if nested := fieldBytes(update, fieldThinkingDelta); len(nested) > 0 {
		if inner, innerErr := decodeFields(nested); innerErr == nil {
			if delta := fieldString(inner, fieldUpdateText); delta != "" {
				if !sendCursorEvent(ctx, events, hyprovider.Event{Kind: hyprovider.EventThinkingDelta, Thinking: delta}) {
					return ctx.Err()
				}
			}
		}
	}
	if nested := fieldBytes(update, fieldTokenDelta); len(nested) > 0 {
		inner, innerErr := decodeFields(nested)
		if innerErr != nil {
			return innerErr
		}
		usage.sawTokenDelta = true
		usage.outputTokens += int(fieldUint32(inner, fieldUpdateTokens))
	}
	if nested := fieldBytes(update, fieldToolCallCompleted); len(nested) > 0 {
		callID, snapshot, providerError, todo, decodeErr := decodeTodoCompletion(nested)
		if decodeErr != nil {
			return decodeErr
		}
		if todo {
			result := HostResult{Content: providerError, IsError: providerError != ""}
			if todoSync != nil {
				result = todoSync(ctx, snapshot, callID, providerError)
			} else if !result.IsError {
				result = HostResult{Content: "Cursor Todo sync is unavailable", IsError: true}
			}
			arguments, _ := json.Marshal(snapshot)
			usage.recordNativeExec(message.ToolCall{ID: callID, Name: "update_todos", Arguments: arguments}, result)
		}
	}
	if hasField(update, fieldTurnEnded) {
		*sawTurnEnded = true
	}
	return nil
}

func replyBlob(writer *io.PipeWriter, payload []byte, blobs *blobStore) error {
	fields, err := decodeFields(payload)
	if err != nil {
		return err
	}
	id := fieldUint32(fields, fieldKVID)
	if args := fieldBytes(fields, fieldKVGetArgs); len(args) > 0 {
		argFields, decodeErr := decodeFields(args)
		if decodeErr != nil {
			return decodeErr
		}
		result := []byte(nil)
		if blob := blobs.get(fieldBytes(argFields, fieldBlobID)); blob != nil {
			result = encodeBytes(result, fieldBlobData, blob)
		}
		reply := encodeUint32(nil, fieldKVID, id)
		reply = encodeBytes(reply, fieldKVGetResult, result)
		_, err = writer.Write(encodeConnectFrame(encodeMessage(nil, fieldAgentKVClient, reply), false))
		return err
	}
	if args := fieldBytes(fields, fieldKVSetArgs); len(args) > 0 {
		argFields, decodeErr := decodeFields(args)
		if decodeErr != nil {
			return decodeErr
		}
		result := []byte(nil)
		if setErr := blobs.setRemote(fieldBytes(argFields, fieldBlobID), fieldBytes(argFields, fieldSetBlobData)); setErr != nil {
			detail := encodeString(nil, fieldCursorErrorMessage, setErr.Error())
			result = encodeMessage(nil, fieldSetBlobResultError, detail)
		}
		reply := encodeUint32(nil, fieldKVID, id)
		reply = encodeBytes(reply, fieldKVSetResult, result)
		_, err = writer.Write(encodeConnectFrame(encodeMessage(nil, fieldAgentKVClient, reply), false))
		return err
	}
	return nil
}

func hasField(fields []protoField, number int) bool {
	for _, field := range fields {
		if field.Field == number {
			return true
		}
	}
	return false
}

func nextConnectFrame(payload []byte) (frame, rest []byte, ok bool, err error) {
	if len(payload) < 5 {
		return nil, payload, false, nil
	}
	length := int(payload[1])<<24 | int(payload[2])<<16 | int(payload[3])<<8 | int(payload[4])
	if length > maxConnectFrameBytes {
		return nil, nil, false, fmt.Errorf("cursor connect frame exceeds %d bytes", maxConnectFrameBytes)
	}
	end := 5 + length
	if end > len(payload) {
		return nil, payload, false, nil
	}
	if payload[0]&connectFlagCompressed != 0 {
		return nil, nil, false, fmt.Errorf("cursor connect frame is compressed")
	}
	if payload[0]&connectFlagEndStream != 0 {
		rest = payload[end:]
		if len(rest) > 0 {
			return nil, nil, false, fmt.Errorf("cursor connect end frame has trailing data")
		}
		return nil, rest, false, parseConnectEndStream(payload[5:end])
	}
	return payload[5:end], payload[end:], true, nil
}

func parseConnectEndStream(payload []byte) error {
	var envelope struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return &hyprovider.Error{Provider: "cursor", Kind: hyprovider.ErrorInvalidRequest, Code: "invalid_connect_end", Message: "failed to parse Connect end stream"}
	}
	if envelope.Error == nil {
		return nil
	}
	code := strings.ToLower(strings.TrimSpace(envelope.Error.Code))
	message := strings.TrimSpace(envelope.Error.Message)
	if message == "" {
		message = "unknown Cursor Connect error"
	}
	kind := hyprovider.ErrorUnknown
	switch code {
	case "unauthenticated":
		kind = hyprovider.ErrorAuthentication
	case "permission_denied":
		kind = hyprovider.ErrorPermission
	case "invalid_argument", "failed_precondition", "out_of_range", "unimplemented":
		kind = hyprovider.ErrorInvalidRequest
	case "not_found":
		kind = hyprovider.ErrorNotFound
	case "resource_exhausted", "aborted", "internal", "unavailable", "data_loss":
		kind = hyprovider.ErrorServer
	case "cancelled", "deadline_exceeded":
		kind = hyprovider.ErrorStream
	}
	return &hyprovider.Error{Provider: "cursor", Kind: kind, Code: code, Message: message}
}

func isCursorResourceExhausted(err error) bool {
	var providerErr *hyprovider.Error
	return errors.As(err, &providerErr) && providerErr.Provider == "cursor" && providerErr.Code == "resource_exhausted"
}

type eventStream struct {
	events chan hyprovider.Event
	close  func()
	once   sync.Once
}

func (s *eventStream) Recv() (hyprovider.Event, error) {
	event, ok := <-s.events
	if !ok {
		return hyprovider.Event{}, io.EOF
	}
	if event.Kind == hyprovider.EventError && event.Err != nil {
		return event, event.Err
	}
	return event, nil
}

func (s *eventStream) Close() error {
	s.once.Do(s.close)
	return nil
}

var (
	_ hyprovider.Driver                 = (*Driver)(nil)
	_ hyprovider.RetryObservable        = (*Driver)(nil)
	_ hyprovider.RetryDelayConfigurable = (*Driver)(nil)
)
