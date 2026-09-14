package devin

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/Viking602/azem/internal/provider/catalog"
	"github.com/Viking602/azem/internal/provider/toolnames"
	"github.com/Viking602/venat/message"
	hyprovider "github.com/Viking602/venat/provider"
)

type Driver struct {
	token   func(context.Context) (string, error)
	account string
	model   catalog.Model
	client  *http.Client
	baseURL string
}

func New(token func(context.Context) (string, error), account string, model catalog.Model) (*Driver, error) {
	if token == nil || account == "" || model.ID == "" {
		return nil, fmt.Errorf("Devin requires an account, token resolver, and model")
	}
	return &Driver{token: token, account: account, model: model, client: newHTTPClient(), baseURL: DefaultAPIURL}, nil
}

func (d *Driver) Metadata() hyprovider.Metadata {
	return hyprovider.Metadata{Name: "devin-agent", Models: []string{d.model.ID}, Version: "2"}
}

func (d *Driver) Stream(ctx context.Context, request hyprovider.Request) (hyprovider.Stream, error) {
	if request.Model != d.model.ID {
		return nil, fmt.Errorf("Devin request model does not match the selected account model")
	}
	token, err := d.token(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("Devin login is required")
	}
	cascade := conversationID(d.account, request)
	wireTools, names := nativeTools(request.Tools, request.Messages)
	system, prompts, err := buildPrompts(ctx, request, cascade, names)
	if err != nil {
		return nil, err
	}
	var authRequest proto
	authRequest.data(1, metadata(token, "", false))
	auth, err := unary(ctx, d.client, d.baseURL+authPath, authRequest)
	if err != nil {
		return nil, err
	}
	jwt, custom := auth.text(1), auth.text(2)
	if auth.err != nil {
		return nil, auth.err
	}
	if jwt == "" {
		return nil, fmt.Errorf("Devin authentication returned no user JWT")
	}
	base, err := apiServer(d.baseURL, custom)
	if err != nil {
		return nil, err
	}
	model, assignment := request.Model, ""
	if d.model.DevinRouter {
		var assign proto
		assign.data(1, metadata(token, "", false))
		assign.text(2, model)
		assign.text(3, cascade)
		for i := len(request.Messages) - 1; i >= 0; i-- {
			if request.Messages[i].Role == message.RoleUser {
				// Late host context also uses USER on the wire, but must never
				// replace the actual user action used by the Adaptive router.
				assign.data(5, prompts[i-(len(request.Messages)-len(prompts))])
				break
			}
		}
		res, err := unary(ctx, d.client, base+assignPath, assign)
		if err != nil {
			return nil, err
		}
		assigned := res.child(1)
		model, assignment = assigned.text(2), assigned.text(1)
		if assigned.err != nil {
			return nil, assigned.err
		}
		if model == "" || assignment == "" {
			return nil, fmt.Errorf("Devin model assignment is incomplete")
		}
	}
	wireRequest := request
	wireRequest.Tools = wireTools
	payload, err := buildRequest(wireRequest, token, jwt, cascade, system, prompts, model, assignment, d.model.MaxOutputTokens, d.model.SupportsParallel, names)
	if err != nil {
		return nil, err
	}
	var compressed bytes.Buffer
	if len(payload) > maxFrameBytes {
		return nil, fmt.Errorf("Devin request exceeds the transport byte budget")
	}
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	body := binary.BigEndian.AppendUint32([]byte{1}, uint32(compressed.Len()))
	body = append(body, compressed.Bytes()...)
	streamCtx, cancel := context.WithCancel(ctx)
	httpRequest, err := http.NewRequestWithContext(streamCtx, http.MethodPost, base+chatPath, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, err
	}
	httpRequest.Header.Set("Content-Type", "application/connect+proto")
	httpRequest.Header.Set("Connect-Protocol-Version", "1")
	httpRequest.Header.Set("Connect-Content-Encoding", "gzip")
	httpRequest.Header.Set("Connect-Accept-Encoding", "gzip")
	httpRequest.Header.Set("Accept-Encoding", "identity")
	response, err := d.client.Do(httpRequest)
	if err != nil {
		cancel()
		return nil, err
	}
	if response.StatusCode/100 != 2 {
		response.Body.Close()
		cancel()
		return nil, hyprovider.NewHTTPError("devin", response.StatusCode, "Devin model request was rejected")
	}
	return &eventStream{ctx: streamCtx, cancel: cancel, body: response.Body, tools: request.Tools, names: names, secrets: []string{token, jwt}, calls: make(map[string]*message.ToolCall), state: turnState{Provider: "devin", Model: request.Model, CascadeID: cascade}}, nil
}

type eventStream struct {
	ctx           context.Context
	cancel        context.CancelFunc
	body          io.ReadCloser
	closeOnce     sync.Once
	tools         []message.ToolDefinition
	names         *toolnames.Names
	secrets       []string
	calls         map[string]*message.ToolCall
	order         []string
	activeCall    string
	argumentBytes int
	usage         hyprovider.Usage
	stop          int
	state         turnState
	response      hyprovider.ResponseMetadata
	pending       []hyprovider.Event
	terminal      bool
}

func (s *eventStream) Close() error {
	s.closeOnce.Do(func() { s.cancel(); _ = s.body.Close() })
	return nil
}

func (s *eventStream) Recv() (hyprovider.Event, error) {
	for len(s.pending) == 0 {
		if s.terminal {
			return hyprovider.Event{}, io.EOF
		}
		if err := s.ctx.Err(); err != nil {
			s.Close()
			return hyprovider.Event{}, err
		}
		if err := s.readFrame(); err != nil {
			s.terminal = true
			s.pending = nil
			s.Close()
			return hyprovider.Event{}, err
		}
	}
	event := s.pending[0]
	s.pending = s.pending[1:]
	return event, nil
}

func (s *eventStream) readFrame() error {
	var header [5]byte
	if _, err := io.ReadFull(s.body, header[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("Devin stream ended without a completion trailer: %w", io.ErrUnexpectedEOF)
		}
		return err
	}
	flag, length := header[0], binary.BigEndian.Uint32(header[1:])
	if flag & ^byte(3) != 0 || length > maxFrameBytes {
		return fmt.Errorf("invalid Devin Connect frame")
	}
	raw := make([]byte, int(length))
	if _, err := io.ReadFull(s.body, raw); err != nil {
		return err
	}
	var err error
	if flag&1 != 0 {
		raw, err = decompress(raw)
	}
	if err != nil {
		return err
	}
	if flag&2 != 0 {
		var trailer struct {
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &trailer) != nil {
			return fmt.Errorf("invalid Devin completion trailer")
		}
		if trailer.Error != nil {
			status := http.StatusBadGateway
			switch trailer.Error.Code {
			case "unauthenticated":
				status = http.StatusUnauthorized
			case "permission_denied":
				status = http.StatusForbidden
			case "resource_exhausted":
				status = http.StatusTooManyRequests
			case "invalid_argument":
				status = http.StatusBadRequest
			}
			return rejectionError(status, trailer.Error.Code, trailer.Error.Message, s.secrets...)
		}
		return s.finish()
	}
	m := decodeProto(raw, nil)
	text, thinking, signature := m.text(3), m.text(9), m.text(10)
	if m.number(8) != 0 {
		text = ""
	}
	if m.number(11) != 0 {
		thinking, signature = "", ""
	}
	if id := m.text(1); id != "" {
		s.state.MessageID, s.response.ID = id, id
	}
	if signatureType := m.text(21); signatureType != "" {
		s.state.SignatureType = signatureType
	}
	if model := m.text(23); model != "" {
		s.response.Model = model
	}
	if id := m.text(15); id != "" {
		s.state.OutputID = id
	}
	if id := m.text(16); id != "" {
		s.state.ThinkingID = id
	}
	if signature := m.data(20); len(signature) > 0 {
		s.state.GeminiSignature = signature
	}
	if phase := m.text(25); phase != "" {
		s.state.Phase = phase
	}
	if stop := m.number(5); stop != 0 {
		s.stop = stop
	}
	if text != "" {
		phase := hyprovider.TextPhase(s.state.Phase)
		if phase != hyprovider.TextPhaseCommentary && phase != hyprovider.TextPhaseFinalAnswer {
			phase = ""
		}
		s.pending = append(s.pending, hyprovider.Event{Kind: hyprovider.EventTextDelta, Text: text, TextPhase: phase})
	}
	if thinking != "" || signature != "" {
		s.pending = append(s.pending, hyprovider.Event{Kind: hyprovider.EventThinkingDelta, Thinking: thinking, Signature: signature})
	}
	for _, field := range m.values(6, 2) {
		call := decodeProto(field.data, m.budget)
		id, name, args := call.text(1), call.text(2), call.text(3)
		name = s.names.Local(name)
		if call.text(4) != "" || call.text(5) != "" {
			return fmt.Errorf("Devin returned invalid tool arguments")
		}
		if call.err != nil {
			return call.err
		}
		if id == "" {
			id = s.activeCall
		}
		if id == "" {
			return fmt.Errorf("Devin tool delta has no call ID")
		}
		current := s.calls[id]
		if current == nil {
			if len(s.calls) >= 1024 {
				return fmt.Errorf("Devin tool-call limit exceeded")
			}
			current = &message.ToolCall{ID: id, Name: name}
			s.calls[id], s.order = current, append(s.order, id)
		}
		if name != "" && current.Name != "" && name != current.Name {
			return fmt.Errorf("Devin tool-call identity changed")
		}
		if name != "" {
			current.Name = name
		}
		s.activeCall = id
		previous := string(current.Arguments)
		delta := args
		if strings.HasPrefix(args, previous) {
			delta = args[len(previous):]
		}
		s.argumentBytes += len(delta)
		if s.argumentBytes > maxFrameBytes {
			return fmt.Errorf("Devin tool arguments exceed the response budget")
		}
		current.Arguments = append(current.Arguments, delta...)
		if delta != "" || name != "" {
			s.pending = append(s.pending, hyprovider.Event{Kind: hyprovider.EventToolCallDelta, ToolCallDelta: &hyprovider.ToolCallDelta{ID: id, Name: current.Name, ArgumentsDelta: delta}})
		}
	}
	if len(m.data(7)) > 0 {
		u := m.child(7)
		input, output, write, read := u.number(2), u.number(3), u.number(4), u.number(5)
		if u.err != nil {
			return u.err
		}
		s.usage = hyprovider.Usage{InputTokens: input + read + write, OutputTokens: output, CachedInputTokens: read, CachedInputTokensReported: true, CacheWriteInputTokens: write, CacheWriteInputTokensReported: true, TotalTokens: input + read + write + output}
	}
	return m.err
}

func (s *eventStream) finish() error {
	switch s.stop {
	case 1, 7, 9, 11, 13:
		return fmt.Errorf("Devin model did not complete the response (stop reason %d)", s.stop)
	case 10:
		if len(s.order) == 0 {
			return fmt.Errorf("Devin completed tool use without a tool call")
		}
	}
	for _, id := range s.order {
		call := s.calls[id]
		known := false
		for _, definition := range s.tools {
			if call.Name == definition.Name {
				known = true
				break
			}
		}
		if !known || !json.Valid(call.Arguments) || !bytes.HasPrefix(bytes.TrimSpace(call.Arguments), []byte("{")) {
			return fmt.Errorf("Devin returned an invalid or unknown tool call")
		}
	}
	if (s.stop == 3 || s.stop == 5) && len(s.order) > 0 {
		return fmt.Errorf("Devin exhausted output tokens before tool completion")
	}
	for _, id := range s.order {
		call := *s.calls[id]
		s.state.ToolCalls = append(s.state.ToolCalls, message.ToolCall{ID: call.ID, Name: s.names.Wire(call.Name), Arguments: append(json.RawMessage(nil), call.Arguments...)})
		args, err := localToolArguments(call.Name, call.Arguments, s.names)
		if err != nil {
			return &hyprovider.Error{Provider: "devin", Kind: hyprovider.ErrorInvalidRequest, Message: err.Error()}
		}
		call.Arguments = args
		s.pending = append(s.pending, hyprovider.Event{Kind: hyprovider.EventToolCall, ToolCall: &call})
	}
	reason := hyprovider.StopReasonComplete
	if s.stop == 3 || s.stop == 5 {
		reason = hyprovider.StopReasonLength
	} else if len(s.order) > 0 {
		reason = hyprovider.StopReasonToolUse
	}
	state, err := json.Marshal(s.state)
	if err != nil {
		return err
	}
	s.pending = append(s.pending, hyprovider.Event{Kind: hyprovider.EventDone, StopReason: reason, Usage: s.usage, ProviderState: state, Response: s.response})
	s.terminal = true
	s.Close()
	return nil
}

var _ hyprovider.Driver = (*Driver)(nil)
