package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/tool"
)

const ToolHub = "hub"

var hubReadOnlyOps = map[string]bool{
	"wait": true, "inbox": true, "list": true, "jobs": true, "cancel": true,
	"ps": true, "logs": true, "describe": true,
}

type HubPeerRequest struct {
	Operation string
	Caller    Invocation
	Params    map[string]any
}

type HubPeerResponse struct {
	Content string
	Details json.RawMessage
	IsError bool
}

type HubPeerBroker interface {
	ExecuteHubPeer(context.Context, HubPeerRequest) (HubPeerResponse, error)
}

type hubPeerBrokerRef struct {
	mu     sync.RWMutex
	broker HubPeerBroker
}

func (ref *hubPeerBrokerRef) set(broker HubPeerBroker) {
	if ref == nil {
		return
	}
	ref.mu.Lock()
	ref.broker = broker
	ref.mu.Unlock()
}

func (ref *hubPeerBrokerRef) get() HubPeerBroker {
	if ref == nil {
		return nil
	}
	ref.mu.RLock()
	defer ref.mu.RUnlock()
	return ref.broker
}

type hubDriver struct {
	root   string
	bridge *lspBridgeRuntime
	jobs   *backgroundJobManager
	peers  *hubPeerBrokerRef
}

type hubToolResult struct {
	Operation string          `json:"operation"`
	Content   string          `json:"content"`
	Details   json.RawMessage `json:"details,omitempty"`
}

func newHubDriver(root string, bridge *lspBridgeRuntime, jobs ...*backgroundJobManager) *hubDriver {
	if bridge == nil {
		bridge = newLSPBridgeRuntime()
	}
	var manager *backgroundJobManager
	if len(jobs) > 0 {
		manager = jobs[0]
	}
	return &hubDriver{root: root, bridge: bridge, jobs: manager}
}

func (driver *hubDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{
		Name:        ToolHub,
		Description: "Coordinate live agent peers, background jobs, and supervised processes. Peer ops: list roster; send with to/message and optional replyTo/await; inbox with optional peek; wait with from. Peer messages enter the recipient's live turn as private untrusted collaborator evidence. Process ops use name: start, ps, logs, wait, send, stop, restart, describe. Jobs use jobs/wait/cancel. Names are stable per project.",
		InputSchema: tool.Schema{
			Type: "object", Required: []string{"op"}, AdditionalProperties: &additional,
			Properties: map[string]tool.Schema{
				"op": {Type: "string"}, "to": {Type: "string"}, "message": {Type: "string"}, "replyTo": {Type: "string"},
				"await": {Type: "boolean"}, "from": {Type: "string"}, "ids": {Type: "array", Items: &tool.Schema{Type: "string"}},
				"timeoutMs": {Type: "integer"}, "peek": {Type: "boolean"}, "name": {Type: "string"}, "application": {Type: "string"},
				"args": {Type: "array", Items: &tool.Schema{Type: "string"}}, "env": {Type: "object"}, "cwd": {Type: "string"},
				"pty": {Type: "boolean"}, "ready": {Type: "object"}, "restart": {Type: "string"}, "persist": {Type: "boolean"},
				"detached": {Type: "boolean"}, "lines": {Type: "integer"}, "head": {Type: "boolean"}, "grep": {Type: "string"},
				"follow": {Type: "boolean"}, "cursor": {Type: "integer"}, "for": {Type: "string"}, "pattern": {Type: "string"},
				"text": {Type: "string"}, "enter": {Type: "boolean"}, "keys": {Type: "array", Items: &tool.Schema{Type: "string"}},
				"signal": {Type: "string"}, "timeout": {Type: "integer"},
			},
		},
		Concurrency: tool.ConcurrencyParallel,
	}
}

func (driver *hubDriver) PolicyForCall(call tool.Call) agentruntime.ToolPolicy {
	policy := agentruntime.ToolPolicy{
		Effect: agentruntime.ToolEffectReadOnly, RiskLevel: "low",
		PolicyTags: []string{"hub", "process", "coordination"}, Concurrency: tool.ConcurrencyParallel,
	}
	var input struct {
		Operation string `json:"op"`
		Name      string `json:"name"`
		To        string `json:"to"`
	}
	_ = json.Unmarshal(call.Arguments, &input)
	op := strings.ToLower(strings.TrimSpace(input.Operation))
	readOnly := hubReadOnlyOps[op] || op == "send" && strings.TrimSpace(input.Name) == "" && strings.TrimSpace(input.To) != ""
	if !readOnly {
		policy.Effect = agentruntime.ToolEffectExternalSideEffect
		policy.RequiresApproval = true
		policy.RequiresActionTask = true
		policy.RiskLevel = "high"
	}
	return policy
}

func (driver *hubDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var params map[string]any
	if err := json.Unmarshal(call.Arguments, &params); err != nil {
		return hubError(call, fmt.Errorf("decode arguments: %w", err)), nil
	}
	op, _ := params["op"].(string)
	op = strings.ToLower(strings.TrimSpace(op))
	valid := map[string]bool{"send": true, "wait": true, "inbox": true, "list": true, "jobs": true, "cancel": true, "start": true, "ps": true, "logs": true, "stop": true, "restart": true, "describe": true}
	if !valid[op] {
		return hubError(call, fmt.Errorf("unsupported op %q", op)), nil
	}
	params["op"] = op
	if err := validateHubParams(driver.root, op, params); err != nil {
		return hubError(call, err), nil
	}
	caller, _ := InvocationFromContext(ctx)
	owner := firstString(caller.AgentID, "Main")
	if broker := driver.peers.get(); broker != nil && isPeerHubOperation(op, params) {
		response, err := broker.ExecuteHubPeer(ctx, HubPeerRequest{Operation: op, Caller: caller, Params: params})
		if err != nil {
			return hubError(call, err), nil
		}
		return hubPeerResult(call, op, response), nil
	}
	if op == "jobs" && driver.jobs != nil {
		return hubJobResult(call, op, driver.jobs.snapshots(owner)), nil
	}
	if op == "cancel" && driver.jobs != nil {
		ids := stringSliceParam(params["ids"])
		if len(ids) == 0 {
			return hubError(call, errors.New("ids is required for cancel")), nil
		}
		return hubJobResult(call, op, driver.jobs.cancelJobs(owner, ids)), nil
	}
	if op == "wait" && driver.jobs != nil {
		name, _ := params["name"].(string)
		from, _ := params["from"].(string)
		if name == "" && from == "" {
			ids := stringSliceParam(params["ids"])
			timeout := 30 * time.Second
			if raw, supplied := params["timeoutMs"]; supplied {
				if value, ok := raw.(float64); ok {
					timeout = time.Duration(value) * time.Millisecond
				}
			}
			return hubJobResult(call, op, driver.jobs.wait(ctx, owner, ids, timeout)), nil
		}
	}
	sessionID := caller.SessionID
	if sessionID == "" {
		sessionID = caller.TeamRunID
	}
	if sessionID == "" {
		sessionID = driver.root
	}
	params["__agentId"] = firstString(caller.AgentID, "Main")
	response, err := driver.bridge.requestHub(ctx, driver.root, sessionID, params)
	if err != nil {
		return hubError(call, err), nil
	}
	isError := response.IsError
	if len(response.Details) > 0 {
		var details struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(response.Details, &details) == nil && strings.TrimSpace(details.Error) != "" {
			isError = true
		}
	}
	result := hubToolResult{Operation: op, Content: response.Content, Details: response.Details}
	structured, _ := json.Marshal(result)
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: response.Content, Structured: structured, IsError: isError}, nil
}

func stringSliceParam(value any) []string {
	raw, _ := value.([]any)
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok && text != "" {
			result = append(result, text)
		}
	}
	return result
}

func isPeerHubOperation(op string, params map[string]any) bool {
	name, _ := params["name"].(string)
	to, _ := params["to"].(string)
	from, _ := params["from"].(string)
	switch op {
	case "list", "inbox":
		return name == ""
	case "send":
		return name == "" && strings.TrimSpace(to) != ""
	case "wait":
		return name == "" && strings.TrimSpace(from) != ""
	default:
		return false
	}
}

func hubPeerResult(call tool.Call, op string, response HubPeerResponse) tool.Result {
	result := hubToolResult{Operation: op, Content: response.Content, Details: response.Details}
	structured, _ := json.Marshal(result)
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: response.Content, Structured: structured, IsError: response.IsError}
}

func hubJobResult(call tool.Call, op string, jobs []backgroundJobSnapshot) tool.Result {
	var output strings.Builder
	if len(jobs) == 0 {
		output.WriteString("No background jobs.")
	} else {
		for index, job := range jobs {
			if index > 0 {
				output.WriteByte('\n')
			}
			fmt.Fprintf(&output, "%s [%s] — %s · %s", job.ID, job.Type, job.Status, job.Label)
			if job.ResultText != "" {
				output.WriteString("\n" + job.ResultText)
			}
			if job.ErrorText != "" {
				output.WriteString("\nError: " + job.ErrorText)
			}
		}
	}
	structured, _ := json.Marshal(map[string]any{"op": op, "jobs": jobs})
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: output.String(), Structured: structured}
}

func validateHubParams(root, op string, params map[string]any) error {
	name, _ := params["name"].(string)
	if name != "" && (len(name) > 48 || !regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(name)) {
		return errors.New("process name must use 1-48 letters, digits, dot, underscore, or dash")
	}
	processOp := op == "start" || op == "ps" || op == "logs" || op == "stop" || op == "restart" || op == "describe" || op == "send" && name != "" || op == "wait" && name != ""
	if processOp && op != "ps" && name == "" && op != "start" {
		return fmt.Errorf("name is required for process op %s", op)
	}
	if op == "start" {
		if name == "" {
			return errors.New("name is required for start")
		}
		application, _ := params["application"].(string)
		if strings.TrimSpace(application) == "" || strings.ContainsAny(application, "\r\n\x00") {
			return errors.New("application is required and must be one executable path/name")
		}
		if strings.ContainsAny(application, `/\\`) {
			path := application
			if !filepath.IsAbs(path) {
				path = filepath.Join(root, filepath.FromSlash(path))
			}
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				return errors.New("application path is not a regular file")
			}
			params["application"] = path
		} else if _, err := exec.LookPath(application); err != nil {
			return fmt.Errorf("application %q is not on PATH", application)
		}
	}
	if op == "send" && name == "" {
		to, _ := params["to"].(string)
		body, _ := params["message"].(string)
		if strings.TrimSpace(to) == "" {
			return errors.New("to is required for peer send")
		}
		if len(to) > 64 || strings.ContainsAny(to, "\r\n\x00") {
			return errors.New("peer recipient is invalid")
		}
		if strings.TrimSpace(body) == "" || len(body) > 128<<10 || strings.ContainsRune(body, '\x00') {
			return errors.New("peer message is required and must not exceed 128 KiB")
		}
		if await, _ := params["await"].(bool); await && to == "all" {
			return errors.New("await is unavailable for broadcast send")
		}
	}
	if cwd, ok := params["cwd"].(string); ok && cwd != "" {
		absolute, _, info, err := secureReadPath(root, cwd)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errors.New("cwd is not a directory")
		}
		params["cwd"] = absolute
	}
	if environment, ok := params["env"].(map[string]any); ok {
		if len(environment) > 256 {
			return errors.New("env has more than 256 entries")
		}
		for key, value := range environment {
			text, ok := value.(string)
			if !ok || key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(text, '\x00') || len(key)+len(text) > 32768 {
				return errors.New("env contains an invalid entry")
			}
		}
	}
	if args, ok := params["args"].([]any); ok {
		if len(args) > 1024 {
			return errors.New("args has more than 1024 entries")
		}
		for _, argument := range args {
			text, ok := argument.(string)
			if !ok || len(text) > 32768 || strings.ContainsRune(text, '\x00') {
				return errors.New("args contains an invalid entry")
			}
		}
	}
	for _, key := range []string{"grep", "pattern"} {
		if expression, ok := params[key].(string); ok {
			if len(expression) > 4096 || strings.Contains(expression, "(?") {
				return fmt.Errorf("%s is invalid; JavaScript regexes do not accept PCRE inline modifiers", key)
			}
		}
	}
	if restart, ok := params["restart"].(string); ok && restart != "" && restart != "no" && restart != "on-failure" && restart != "always" {
		return errors.New("restart must be no, on-failure, or always")
	}
	if signal, ok := params["signal"].(string); ok && signal != "" {
		valid := map[string]bool{"SIGINT": true, "SIGTERM": true, "SIGHUP": true, "SIGQUIT": true, "SIGKILL": true}
		if !valid[signal] {
			return errors.New("signal is invalid")
		}
	}
	if lines, ok := params["lines"].(float64); ok && (lines < 1 || lines > 1000 || lines != float64(int(lines))) {
		return errors.New("lines must be an integer from 1 to 1000")
	}
	for _, key := range []string{"timeout", "timeoutMs", "cursor"} {
		if value, ok := params[key].(float64); ok && (value < 0 || value != float64(int(value)) || value > 3_600_000) {
			return fmt.Errorf("%s is outside the supported range", key)
		}
	}
	if ready, ok := params["ready"].(map[string]any); ok {
		if port, ok := ready["port"].(float64); ok && (port < 1 || port > 65535 || port != float64(int(port))) {
			return errors.New("ready.port must be 1-65535")
		}
		if expression, ok := ready["log"].(string); ok && (expression == "" || len(expression) > 4096 || strings.Contains(expression, "(?")) {
			return errors.New("ready.log is an invalid JavaScript regex")
		}
	}
	return nil
}

func hubError(call tool.Call, err error) tool.Result {
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: "hub failed: " + err.Error(), IsError: true}
}
