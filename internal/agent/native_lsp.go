package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/tool"
)

const ToolLSP = "lsp"

type lspDriver struct {
	root          string
	host          *nativeHost
	allowWrite    bool
	edit          tool.Driver
	networkPolicy string
}

func (*lspDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{Name: ToolLSP, Description: "Native stdio Language Server Protocol client. Supports Go (gopls), Rust (rust-analyzer), C/C++ (clangd), and Python (pylsp); install the corresponding executable. Positions are 1-based lines/columns, measured in Unicode characters. rename/code_actions return previews; patch:true prepares a Hashline patch for review and execution with coding.edit_hashline. request accepts a protocol method and params. No TS language server or runtime is bundled.", InputSchema: tool.Schema{Type: "object", Required: []string{"action"}, AdditionalProperties: &additional, Properties: map[string]tool.Schema{
		"action": {Type: "string", Enum: []string{"status", "reload", "capabilities", "diagnostics", "definition", "type_definition", "implementation", "references", "hover", "symbols", "rename", "code_actions", "format", "request"}},
		"file":   {Type: "string"}, "line": {Type: "integer"}, "column": {Type: "integer"}, "symbol": {Type: "string"}, "query": {Type: "string"}, "new_name": {Type: "string"}, "patch": {Type: "boolean"}, "method": {Type: "string"}, "params": {Type: "object"}, "timeout": {Type: "integer", Description: "1-300 seconds, default 60."},
	}}, Concurrency: tool.ConcurrencyParallel}
}

func (*lspDriver) ToolPolicy() agentruntime.ToolPolicy {
	// Language servers can run project build scripts and compiler plugins.
	return approvedExternalPolicy("coding", "lsp", "execute")
}

func nativeLanguage(file string) (string, string, error) {
	switch strings.ToLower(filepath.Ext(file)) {
	case ".go":
		return "gopls", "go", nil
	case ".rs":
		return "rust-analyzer", "rust", nil
	case ".c", ".h":
		return "clangd", "c", nil
	case ".cc", ".cpp", ".cxx", ".hpp":
		return "clangd", "cpp", nil
	case ".py":
		return "pylsp", "python", nil
	default:
		return "", "", errors.New("file must select a supported native language server: .go, .rs, .c/.cpp, or .py")
	}
}

func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func (p *nativeProcess) rpc(method string, params any) (json.RawMessage, error) {
	p.seq++
	id := p.seq
	if err := p.writeFrame(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for count := 0; count < 4096; count++ {
		payload, err := p.readFrame()
		if err != nil {
			return nil, err
		}
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return nil, err
		}
		if envelope.Method != "" {
			if len(envelope.ID) > 0 {
				response := map[string]any{"jsonrpc": "2.0", "id": envelope.ID}
				switch envelope.Method {
				case "workspace/configuration":
					var config struct {
						Items []json.RawMessage `json:"items"`
					}
					_ = json.Unmarshal(envelope.Params, &config)
					response["result"] = make([]any, len(config.Items))
				case "client/registerCapability", "window/workDoneProgress/create":
					response["result"] = nil
				default:
					response["error"] = map[string]any{"code": -32601, "message": "client request is unsupported; tools cannot apply edits or execute commands through reverse requests"}
				}
				if err := p.writeFrame(response); err != nil {
					return nil, err
				}
			} else if envelope.Method == "textDocument/publishDiagnostics" {
				if len(p.events) == 32 {
					p.events = p.events[1:]
				}
				if len(payload) < 128<<10 {
					p.events = append(p.events, payload)
				}
			}
			continue
		}
		if string(envelope.ID) != strconv.Itoa(id) {
			continue
		}
		if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
			return nil, fmt.Errorf("LSP %s: %s", method, envelope.Error)
		}
		return envelope.Result, nil
	}
	return nil, errors.New("language server exceeded response message budget")
}

func (d *lspDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		Action  string         `json:"action"`
		File    string         `json:"file"`
		Line    int            `json:"line"`
		Column  int            `json:"column"`
		Symbol  string         `json:"symbol"`
		Query   string         `json:"query"`
		NewName string         `json:"new_name"`
		Patch   bool           `json:"patch"`
		Method  string         `json:"method"`
		Params  map[string]any `json:"params"`
		Timeout int            `json:"timeout"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return toolError(call, err.Error()), nil
	}
	if input.Timeout < 0 || input.Timeout > 300 {
		return toolError(call, "timeout must be 1-300"), nil
	}
	if d.networkPolicy == "deny" {
		return toolError(call, "language servers cannot enforce network denial; use a network-isolated host"), nil
	}
	if len(call.Arguments) > 1<<20 {
		return toolError(call, "LSP arguments exceed 1 MiB"), nil
	}
	if input.Timeout == 0 {
		input.Timeout = 60
	}
	if input.Patch && (!d.allowWrite || d.edit == nil) {
		return toolError(call, "LSP writes are disabled"), nil
	}
	program, language, err := nativeLanguage(input.File)
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	absolute, relative, info, err := secureReadPath(d.root, input.File)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		if err == nil {
			err = errors.New("LSP file must be regular and at most 1 MiB")
		}
		return toolError(call, err.Error()), nil
	}
	text, err := nativeWorkspaceFile(d.root, relative, 1<<20)
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	if len(text) > 1<<20 || !utf8.Valid(text) {
		return toolError(call, "LSP source must be UTF-8 and at most 1 MiB"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(input.Timeout)*time.Second)
	defer cancel()
	key := nativeScope(ctx, d.root, "lsp", program)
	if input.Action == "reload" {
		d.host.mu.Lock()
		prior := d.host.processes[key]
		d.host.mu.Unlock()
		if prior != nil {
			prior.close()
			select {
			case <-prior.done:
			case <-ctx.Done():
				return toolError(call, ctx.Err().Error()), nil
			}
		}
	}
	p, err := d.host.start(key, d.root, program, nil, false)
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	unlock, err := p.lock(ctx)
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	defer unlock()
	failed := true
	defer func() {
		if failed {
			p.close()
		}
	}()
	if !p.initialized {
		capabilities, err := p.rpc("initialize", map[string]any{"processId": os.Getpid(), "rootUri": fileURI(d.root), "capabilities": map[string]any{"general": map[string]any{"positionEncodings": []string{"utf-16"}}}})
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		p.data["capabilities"] = string(capabilities)
		if err := p.writeFrame(map[string]any{"jsonrpc": "2.0", "method": "initialized", "params": map[string]any{}}); err != nil {
			return toolError(call, err.Error()), nil
		}
		p.initialized = true
	}
	uri := fileURI(absolute)
	if previous := p.data["open-document"]; previous != "" {
		if err := p.writeFrame(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didClose", "params": map[string]any{"textDocument": map[string]string{"uri": previous}}}); err != nil {
			return toolError(call, err.Error()), nil
		}
	}
	if err := p.writeFrame(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": language, "version": 1, "text": string(text)}}}); err != nil {
		return toolError(call, err.Error()), nil
	}
	p.data["open-document"] = uri
	params := map[string]any{"textDocument": map[string]string{"uri": uri}}
	method := ""
	switch input.Action {
	case "status", "reload", "capabilities":
		failed = false
		return nativeJSONResult(call, map[string]any{"server": program, "pid": p.supervisor.command.Process.Pid, "capabilities": json.RawMessage(p.data["capabilities"])})
	case "diagnostics":
		method = "textDocument/diagnostic"
	case "symbols":
		method = "textDocument/documentSymbol"
		if input.Query != "" {
			method, params = "workspace/symbol", map[string]any{"query": input.Query}
		}
	case "format":
		method = "textDocument/formatting"
		params["options"] = map[string]any{"tabSize": 4, "insertSpaces": true}
	case "request":
		if strings.TrimSpace(input.Method) == "" || strings.HasPrefix(input.Method, "$/") || input.Patch {
			return toolError(call, "request needs method; patch is only supported for rename/format"), nil
		}
		method, params = input.Method, input.Params
	default:
		methods := map[string]string{"definition": "definition", "type_definition": "typeDefinition", "implementation": "implementation", "references": "references", "hover": "hover", "rename": "rename", "code_actions": "codeAction"}
		operation, ok := methods[input.Action]
		if !ok {
			return toolError(call, "unsupported LSP action"), nil
		}
		position, err := nativePosition(string(text), input.Line, input.Column, input.Symbol)
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		method, params["position"] = "textDocument/"+operation, position
		if input.Action == "references" {
			params["context"] = map[string]bool{"includeDeclaration": true}
		}
		if input.Action == "rename" {
			if strings.TrimSpace(input.NewName) == "" {
				return toolError(call, "new_name is required"), nil
			}
			params["newName"] = input.NewName
		}
		if input.Action == "code_actions" {
			delete(params, "position")
			params["range"] = map[string]any{"start": position, "end": position}
			params["context"] = map[string]any{"diagnostics": []any{}}
		}
	}
	result, err := p.rpc(method, params)
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	failed = false
	if input.Patch {
		switch input.Action {
		case "rename":
			return lspWorkspacePatch(ctx, call, d.root, d.edit, result)
		case "format":
			edit, err := json.Marshal(map[string]any{"changes": map[string]any{uri: result}})
			if err != nil {
				return toolError(call, err.Error()), nil
			}
			return lspWorkspacePatch(ctx, call, d.root, d.edit, edit)
		default:
			return toolError(call, "code_actions returns alternatives; apply one explicit WorkspaceEdit with Hashline after reviewing it"), nil
		}
	}
	return nativeJSONResult(call, map[string]any{"action": input.Action, "file": relative, "result": result, "notifications": p.events})
}

func nativePosition(text string, line, column int, symbol string) (map[string]int, error) {
	lines := strings.Split(text, "\n")
	if line < 1 || line > len(lines) {
		return nil, errors.New("line is outside file")
	}
	row := strings.TrimSuffix(lines[line-1], "\r")
	if column == 0 {
		column = 1
	}
	prefix := ""
	if symbol != "" {
		index := strings.Index(row, symbol)
		if index < 0 || strings.Count(row, symbol) != 1 {
			return nil, errors.New("symbol must occur exactly once on the line; otherwise specify column")
		}
		prefix = row[:index]
	} else {
		characters := []rune(row)
		if column < 1 || column > len(characters)+1 {
			return nil, errors.New("column is outside line")
		}
		prefix = string(characters[:column-1])
	}
	return map[string]int{"line": line - 1, "character": len(utf16.Encode([]rune(prefix)))}, nil
}

type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}
type lspTextEdit struct {
	Range struct {
		Start lspPosition `json:"start"`
		End   lspPosition `json:"end"`
	} `json:"range"`
	NewText string `json:"newText"`
}

func lspByteOffset(text string, position lspPosition) (int, error) {
	if position.Line < 0 || position.Character < 0 {
		return 0, errors.New("negative LSP position")
	}
	start := 0
	for line := 0; line < position.Line; line++ {
		next := strings.IndexByte(text[start:], '\n')
		if next < 0 {
			return 0, errors.New("LSP line is outside file")
		}
		start += next + 1
	}
	units := 0
	for index := start; ; {
		if units == position.Character {
			return index, nil
		}
		if index >= len(text) || text[index] == '\n' || text[index] == '\r' {
			break
		}
		char, size := utf8.DecodeRuneInString(text[index:])
		width := 1
		if char > 0xffff {
			width = 2
		}
		units += width
		index += size
		if units > position.Character {
			break
		}
	}
	return 0, errors.New("LSP character splits a surrogate or is outside line")
}
