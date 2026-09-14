package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/Viking602/venat/tool"
)

type nativeFileChange struct{ path, before, after string }

func nativeWorkspaceFile(workspace, relative string, limit int) ([]byte, error) {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(relative)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return nil, fmt.Errorf("workspace input must be a regular file at most %d bytes", limit)
	}
	payload, truncated, err := readLimited(file, limit)
	if truncated {
		return nil, errors.New("workspace input grew beyond size limit")
	}
	return payload, err
}

func applyNativeChanges(ctx context.Context, call tool.Call, edit tool.Driver, changes []nativeFileChange, dryRun bool) (tool.Result, error) {
	if edit == nil {
		return toolError(call, "workspace edits are disabled"), nil
	}
	var patch strings.Builder
	patch.WriteString("*** Begin Patch\n")
	count := 0
	for _, change := range changes {
		if change.before == change.after {
			continue
		}
		if strings.ContainsAny(change.path, "\r\n#[]") {
			return toolError(call, "file name cannot be represented safely by Hashline"), nil
		}
		before, after := strings.Split(normalizeHashlineText([]byte(change.before)), "\n"), strings.Split(normalizeHashlineText([]byte(change.after)), "\n")
		start := 0
		for start < len(before) && start < len(after) && before[start] == after[start] {
			start++
		}
		endBefore, endAfter := len(before), len(after)
		for endBefore > start && endAfter > start && before[endBefore-1] == after[endAfter-1] {
			endBefore--
			endAfter--
		}
		fmt.Fprintf(&patch, "[%s#%s]\n", change.path, computeHashlineTag(normalizeHashlineText([]byte(change.before))))
		if start == endBefore {
			if start == 0 {
				patch.WriteString("PUT <1:\n")
			} else {
				fmt.Fprintf(&patch, "PUT >%d:\n", start)
			}
		} else if start == endAfter {
			fmt.Fprintf(&patch, "CUT %d.=%d\n", start+1, endBefore)
		} else {
			fmt.Fprintf(&patch, "PUT %d.=%d:\n", start+1, endBefore)
		}
		for _, line := range after[start:endAfter] {
			patch.WriteString("+" + line + "\n")
		}
		count++
	}
	if count == 0 {
		return nativeJSONResult(call, map[string]any{"changed": false})
	}
	patch.WriteString("*** End Patch")
	if dryRun {
		return nativeJSONResult(call, map[string]any{"dryRun": true, "files": count, "patch": patch.String()})
	}
	arguments, err := json.Marshal(map[string]string{"input": patch.String()})
	if err != nil {
		return tool.Result{}, err
	}
	result, err := edit.Execute(ctx, tool.Call{ID: call.ID, Name: ToolEditHashline, Arguments: arguments}, nil)
	result.Name = call.Name
	return result, err
}

func lspWorkspacePatch(ctx context.Context, call tool.Call, root string, edit tool.Driver, payload json.RawMessage) (tool.Result, error) {
	var value struct {
		Changes         map[string][]lspTextEdit `json:"changes"`
		DocumentChanges []struct {
			Kind         string `json:"kind"`
			TextDocument struct {
				URI     string `json:"uri"`
				Version *int   `json:"version"`
			} `json:"textDocument"`
			Edits []lspTextEdit `json:"edits"`
		} `json:"documentChanges"`
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return toolError(call, err.Error()), nil
	}
	if value.Changes == nil {
		value.Changes = make(map[string][]lspTextEdit)
	}
	for _, change := range value.DocumentChanges {
		if change.Kind != "" {
			return toolError(call, "LSP file create/rename/delete requires a separately reviewed Hashline edit"), nil
		}
		if change.TextDocument.Version != nil && *change.TextDocument.Version != 1 {
			return toolError(call, "LSP edit is for a different document version"), nil
		}
		if _, exists := value.Changes[change.TextDocument.URI]; exists {
			return toolError(call, "duplicate LSP document edit"), nil
		}
		value.Changes[change.TextDocument.URI] = change.Edits
	}
	if len(value.Changes) > 100 {
		return toolError(call, "LSP edit exceeds 100 files"), nil
	}
	var changes []nativeFileChange
	for rawURI, edits := range value.Changes {
		uri, err := url.Parse(rawURI)
		if err != nil || uri.Scheme != "file" || uri.Host != "" || uri.RawQuery != "" || uri.Fragment != "" {
			return toolError(call, "LSP edit requires a local file URI"), nil
		}
		_, relative, info, err := secureReadPath(root, uri.Path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return toolError(call, "LSP edit path must be a regular workspace file at most 1 MiB"), nil
		}
		content, err := nativeWorkspaceFile(root, relative, 1<<20)
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		after, err := applyLSPTextEdits(string(content), edits)
		if err != nil {
			return toolError(call, err.Error()), nil
		}
		changes = append(changes, nativeFileChange{relative, string(content), after})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].path < changes[j].path })
	// LSP ranges do not carry source hashes. Emit a reviewable Hashline patch;
	// the ordinary edit tool performs the mutation with its stale-tag guard.
	return applyNativeChanges(ctx, call, edit, changes, true)
}

func applyLSPTextEdits(content string, edits []lspTextEdit) (string, error) {
	type replacement struct {
		start, end int
		text       string
	}
	if len(edits) > 10000 {
		return "", errors.New("too many LSP edits")
	}
	var replacements []replacement
	for _, edit := range edits {
		start, err := lspByteOffset(content, edit.Range.Start)
		if err != nil {
			return "", err
		}
		end, err := lspByteOffset(content, edit.Range.End)
		if err != nil {
			return "", err
		}
		if end < start {
			return "", errors.New("reversed LSP edit")
		}
		replacements = append(replacements, replacement{start, end, edit.NewText})
	}
	sort.SliceStable(replacements, func(i, j int) bool { return replacements[i].start < replacements[j].start })
	var output strings.Builder
	offset := 0
	for _, edit := range replacements {
		if edit.start < offset {
			return "", errors.New("overlapping LSP edits")
		}
		output.WriteString(content[offset:edit.start])
		output.WriteString(edit.text)
		offset = edit.end
		if output.Len() > 4<<20 {
			return "", errors.New("LSP edit output exceeds 4 MiB")
		}
	}
	output.WriteString(content[offset:])
	return output.String(), nil
}
