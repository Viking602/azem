package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/agentruntime"
	"github.com/Viking602/venat/tool"
)

const ToolEval = "coding.eval"

type evalDriver struct {
	root                    string
	host                    *nativeHost
	approval, networkPolicy string
}

func (*evalDriver) Definition() tool.Definition {
	additional := false
	return tool.Definition{Name: ToolEval, Description: "Execute Python cells in a persistent Python 3 process scoped to this workspace, conversation and agent. Variables survive subsequent cells. The last expression is returned. Set reset:true to start a fresh kernel. Arbitrary execution uses the same approval boundary as shell; no JavaScript or TypeScript runtime is used.", InputSchema: tool.Schema{Type: "object", AdditionalProperties: &additional, Properties: map[string]tool.Schema{
		"code": {Type: "string"}, "reset": {Type: "boolean"}, "timeout": {Type: "integer", Description: "1-300 seconds, default 30; timeout terminates the kernel."},
	}}, Concurrency: tool.ConcurrencyParallel}
}

func (d *evalDriver) ToolPolicy() agentruntime.ToolPolicy {
	policy := approvedExternalPolicy("coding", "execute", "python")
	policy.RequiresApproval = d.approval != "allow"
	return policy
}

func (d *evalDriver) Execute(ctx context.Context, call tool.Call, _ tool.UpdateSink) (tool.Result, error) {
	var input struct {
		Code    string `json:"code"`
		Reset   bool   `json:"reset"`
		Timeout int    `json:"timeout"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return toolError(call, err.Error()), nil
	}
	if len(input.Code) > 1<<20 || input.Timeout < 0 || input.Timeout > 300 || strings.TrimSpace(input.Code) == "" && !input.Reset {
		return toolError(call, "code is required (max 1 MiB); timeout must be 1-300"), nil
	}
	if d.approval == "deny" {
		return toolError(call, "Python execution is denied by workspace shell policy"), nil
	}
	// Like shell, this is an execution boundary, not an in-process Python sandbox.
	if d.networkPolicy == "deny" {
		return toolError(call, "Python execution cannot enforce network denial; use a network-isolated host"), nil
	}
	if input.Timeout == 0 {
		input.Timeout = 30
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(input.Timeout)*time.Second)
	defer cancel()
	key := nativeScope(ctx, d.root, "eval", "python")
	if input.Reset {
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
	p, err := d.host.start(key, d.root, "python3", []string{"-I", "-u", "-c", nativePythonKernel}, false)
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	unlock, err := p.lock(ctx)
	if err != nil {
		return toolError(call, err.Error()), nil
	}
	defer unlock()
	if err = p.writeFrame(map[string]string{"code": input.Code}); err != nil {
		p.close()
		return toolError(call, err.Error()), nil
	}
	response, err := p.readFrame()
	if err != nil {
		p.close()
		return toolError(call, fmt.Sprintf("Python kernel stopped; variables were lost: %v", err)), nil
	}
	var result struct {
		Output string `json:"output"`
		Value  string `json:"value"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(response, &result); err != nil {
		p.close()
		return toolError(call, err.Error()), nil
	}
	content := strings.TrimSpace(strings.Join([]string{result.Output, result.Value, result.Error}, "\n"))
	if content == "" {
		content = "Cell completed."
	}
	return tool.Result{ToolCallID: call.ID, Name: call.Name, Content: content, Structured: response, IsError: result.Error != ""}, nil
}

const nativePythonKernel = `import ast, contextlib, io, json, os, sys, traceback
wire_in = os.fdopen(os.dup(0), 'rb', buffering=0)
wire_out = os.fdopen(os.dup(1), 'wb', buffering=0)
os.dup2(2, 1)
sys.path.insert(0, os.getcwd())
scope = {'__name__': '__main__'}
class Output(io.TextIOBase):
    def __init__(self): self.parts, self.size = [], 0
    def write(self, text):
        self.size += len(text)
        if self.size > 262144: raise RuntimeError('cell output exceeds 256 KiB')
        self.parts.append(text)
        return len(text)
    def flush(self): pass
while True:
    headers = {}
    while True:
        line = wire_in.readline(8193)
        if not line: sys.exit(0)
        if len(line) > 8192: sys.exit(2)
        if line in (b'\r\n', b'\n'): break
        key, value = line.decode('ascii').split(':', 1)
        headers[key.lower()] = value.strip()
    size = int(headers['content-length'])
    if size < 0 or size > 4194304: sys.exit(2)
    chunks, remaining = [], size
    while remaining:
        chunk = wire_in.read(remaining)
        if not chunk: sys.exit(2)
        chunks.append(chunk); remaining -= len(chunk)
    request = json.loads(b''.join(chunks))
    output, value, error = Output(), '', ''
    try:
        tree = ast.parse(request['code'], '<azem-cell>', 'exec')
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(output):
            expression = tree.body.pop() if tree.body and isinstance(tree.body[-1], ast.Expr) else None
            exec(compile(tree, '<azem-cell>', 'exec'), scope)
            if expression is not None:
                result = eval(compile(ast.Expression(expression.value), '<azem-cell>', 'eval'), scope)
                if result is not None: value = repr(result)[:262144]
    except BaseException:
        error = traceback.format_exc(limit=20)[-65536:]
    payload = json.dumps({'output': ''.join(output.parts), 'value': value, 'error': error}).encode('utf-8')
    wire_out.write(('Content-Length: %d\r\n\r\n' % len(payload)).encode('ascii') + payload)
`
