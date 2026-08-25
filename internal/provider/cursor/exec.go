package cursor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Viking602/venat/message"
)

type execEnvelope struct {
	ID     uint32
	ExecID string
	Kind   int
	Args   []byte
}

func parseExecEnvelope(payload []byte) (execEnvelope, bool) {
	fields, err := decodeFields(payload)
	if err != nil {
		return execEnvelope{}, false
	}
	env := execEnvelope{ID: fieldUint32(fields, fieldExecMessageID), ExecID: fieldString(fields, fieldExecID)}
	for _, kind := range []int{
		fieldExecRequestContext, fieldExecReadArgs, fieldExecWriteArgs, fieldExecShellArgs, fieldExecShellStream,
		fieldExecGrepArgs, fieldExecLsArgs, fieldExecMCPArgs, fieldExecDeleteArgs, fieldExecFetchArgs,
		fieldExecPiReadArgs, fieldExecPiBashArgs, fieldExecPiEditArgs, fieldExecPiWriteArgs,
		fieldExecPiGrepArgs, fieldExecPiFindArgs, fieldExecPiLsArgs,
	} {
		if hasField(fields, kind) {
			env.Kind = kind
			env.Args = fieldBytes(fields, kind)
			return env, true
		}
	}
	return env, env.ID != 0
}

func handleExecServerMessage(ctx context.Context, writer io.Writer, host ExecHost, tools []message.ToolDefinition, payload []byte, resolved func(message.ToolCall, HostResult)) error {
	env, ok := parseExecEnvelope(payload)
	if !ok {
		return nil
	}
	if env.Kind == fieldExecRequestContext {
		return sendExecResult(writer, env, fieldExecClientRequestCtx, encodeRequestContextResult(tools))
	}
	call, mapped := mapExecKind(env)
	if !mapped {
		return sendExecThrow(writer, env, fmt.Sprintf("Unsupported Cursor exec variant %d", env.Kind))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result := HostResult{}
	if host == nil {
		result = HostResult{Content: "Tool not available", IsError: true, Code: DeleteCodeRejected}
	} else {
		var err error
		result, err = host.Execute(ctx, call)
		if err != nil {
			result = HostResult{Content: err.Error(), IsError: true}
		}
	}
	if resolved != nil {
		resolved(call, result)
	}
	if result.IsError {
		return sendMappedReject(writer, env, call, result.Content, result.Code)
	}
	return sendMappedSuccess(writer, env, call, result.Content)
}

func mapExecKind(env execEnvelope) (message.ToolCall, bool) {
	id := firstNonEmpty(env.ExecID, uintString(env.ID))
	switch env.Kind {
	case fieldExecReadArgs, fieldExecPiReadArgs:
		return mapReadArgs(id, env.Args, true)
	case fieldExecWriteArgs, fieldExecPiWriteArgs:
		return mapWriteArgs(id, env.Args)
	case fieldExecShellArgs, fieldExecShellStream, fieldExecPiBashArgs:
		return mapShellArgs(id, env.Args)
	case fieldExecGrepArgs, fieldExecPiGrepArgs:
		return mapGrepArgs(id, env.Args)
	case fieldExecLsArgs, fieldExecPiLsArgs:
		return mapLsArgs(id, env.Args)
	case fieldExecPiFindArgs:
		return mapPiFindArgs(id, env.Args)
	case fieldExecPiEditArgs:
		return mapPiEditArgs(id, env.Args)
	case fieldExecMCPArgs:
		return mapMCPArgs(id, env.Args)
	case fieldExecDeleteArgs:
		return mapDeleteArgs(id, env.Args)
	default:
		return message.ToolCall{}, false
	}
}

func sendMappedSuccess(writer io.Writer, env execEnvelope, call message.ToolCall, content string) error {
	switch env.Kind {
	case fieldExecPiEditArgs:
		return sendExecResult(writer, env, fieldExecClientPiEdit, encodePiEditSuccess(content))
	case fieldExecPiFindArgs:
		return sendExecResult(writer, env, fieldExecClientPiFind, encodePiFindSuccess(content))
	}
	path := jsonString(call.Arguments, "path")
	switch call.Name {
	case azemRead:
		return sendExecResult(writer, env, fieldExecClientRead, encodeReadSuccess(path, content))
	case azemWrite:
		return sendExecResult(writer, env, fieldExecClientWrite, encodeWriteSuccess(path, content))
	case azemShell:
		if env.Kind == fieldExecShellStream {
			return sendShellStream(writer, env, content, 0)
		}
		return sendExecResult(writer, env, fieldExecClientShell, encodeShellSuccess(jsonString(call.Arguments, "command"), content, 0))
	case azemSearch:
		return sendExecResult(writer, env, fieldExecClientGrep, encodeGrepSuccess(jsonString(call.Arguments, "query"), path, content))
	case azemList:
		return sendExecResult(writer, env, fieldExecClientLs, encodeLsSuccess(path, content))
	case azemDelete:
		return sendExecResult(writer, env, fieldExecClientDelete, encodeDeleteSuccess(path, content))
	default:
		return sendExecResult(writer, env, fieldExecClientMCP, encodeMCPSuccess(content, false))
	}
}

func sendMappedReject(writer io.Writer, env execEnvelope, call message.ToolCall, reason, code string) error {
	switch env.Kind {
	case fieldExecPiEditArgs:
		if code == DeleteCodeRejected {
			return sendExecResult(writer, env, fieldExecClientPiEdit, encodePiEditRejected(reason))
		}
		return sendExecResult(writer, env, fieldExecClientPiEdit, encodePiEditError(reason))
	case fieldExecPiFindArgs:
		return sendExecResult(writer, env, fieldExecClientPiFind, encodePiFindError(reason))
	}
	path := jsonString(call.Arguments, "path")
	switch call.Name {
	case azemRead:
		return sendExecResult(writer, env, fieldExecClientRead, encodeReadError(path, reason))
	case azemWrite:
		return sendExecResult(writer, env, fieldExecClientWrite, encodeWriteError(path, reason))
	case azemShell:
		if env.Kind == fieldExecShellStream {
			return sendExecResult(writer, env, fieldExecClientShellStream, encodeShellStreamRejected(reason))
		}
		return sendExecResult(writer, env, fieldExecClientShell, encodeShellRejected(jsonString(call.Arguments, "command"), reason))
	case azemSearch:
		return sendExecResult(writer, env, fieldExecClientGrep, encodeGrepError(reason))
	case azemList:
		return sendExecResult(writer, env, fieldExecClientLs, encodeLsError(reason))
	case azemDelete:
		return sendExecResult(writer, env, fieldExecClientDelete, encodeDeleteFailure(path, reason, code))
	default:
		return sendExecResult(writer, env, fieldExecClientMCP, encodeMCPRejected(reason))
	}
}

func sendExecResult(writer io.Writer, env execEnvelope, resultField int, result []byte) error {
	if err := writeExecClient(writer, env, resultField, result); err != nil {
		return err
	}
	return sendExecClose(writer, env.ID)
}

func writeExecClient(writer io.Writer, env execEnvelope, resultField int, result []byte) error {
	body := encodeUint32(nil, fieldExecClientID, env.ID)
	if env.ExecID != "" {
		body = encodeString(body, fieldExecClientExecID, env.ExecID)
	}
	body = encodeMessage(body, resultField, result)
	return writeConnect(writer, encodeMessage(nil, fieldAgentExecClient, body))
}

func sendExecThrow(writer io.Writer, env execEnvelope, reason string) error {
	throw := encodeUint32(nil, fieldThrowID, env.ID)
	throw = encodeString(throw, fieldThrowError, reason)
	control := encodeMessage(nil, fieldExecControlThrow, throw)
	return writeConnect(writer, encodeMessage(nil, fieldAgentExecClientControl, control))
}

func sendExecClose(writer io.Writer, id uint32) error {
	closeMsg := encodeUint32(nil, fieldCloseID, id)
	control := encodeMessage(nil, fieldExecControlClose, closeMsg)
	return writeConnect(writer, encodeMessage(nil, fieldAgentExecClientControl, control))
}

func sendShellStream(writer io.Writer, env execEnvelope, stdout string, code int32) error {
	if err := writeExecClient(writer, env, fieldExecClientShellStream, encodeMessage(nil, fieldShellStreamStart, nil)); err != nil {
		return err
	}
	if stdout != "" {
		chunk := encodeString(nil, fieldShellStreamData, stdout)
		if err := writeExecClient(writer, env, fieldExecClientShellStream, encodeMessage(nil, fieldShellStreamStdout, chunk)); err != nil {
			return err
		}
	}
	exit := encodeUint32(nil, fieldShellStreamExitCode, uint32(code))
	if err := writeExecClient(writer, env, fieldExecClientShellStream, encodeMessage(nil, fieldShellStreamExit, exit)); err != nil {
		return err
	}
	return sendExecClose(writer, env.ID)
}

func encodeRequestContextResult(tools []message.ToolDefinition) []byte {
	var contextBody []byte
	for _, tool := range tools {
		if def := encodeMCPToolDef(tool); len(def) > 0 {
			contextBody = encodeMessage(contextBody, fieldRequestContextTools, def)
		}
	}
	success := encodeMessage(nil, fieldRequestContextInner, contextBody)
	return encodeMessage(nil, fieldResultSuccess, success)
}

func encodeReadSuccess(path, content string) []byte {
	lines := int32(strings.Count(content, "\n") + 1)
	if content == "" {
		lines = 0
	}
	success := encodeString(nil, fieldReadSuccessPath, path)
	success = encodeString(success, fieldReadSuccessContent, content)
	success = encodeInt32(success, fieldReadSuccessLines, lines)
	success = encodeInt64(success, fieldReadSuccessSize, int64(len(content)))
	return encodeMessage(nil, fieldResultSuccess, success)
}

func encodeReadError(path, err string) []byte {
	body := encodeString(nil, fieldErrorPath, path)
	body = encodeString(body, fieldErrorText, err)
	return encodeMessage(nil, fieldResultError, body)
}

func encodeWriteSuccess(path, content string) []byte {
	lines := int32(strings.Count(content, "\n") + 1)
	success := encodeString(nil, fieldWriteSuccessPath, path)
	success = encodeInt32(success, fieldWriteSuccessLines, lines)
	success = encodeInt32(success, fieldWriteSuccessSize, int32(len(content)))
	return encodeMessage(nil, fieldResultSuccess, success)
}

func encodeWriteError(path, err string) []byte {
	body := encodeString(nil, fieldErrorPath, path)
	body = encodeString(body, fieldErrorText, err)
	return encodeMessage(nil, fieldResultError, body)
}

func encodeShellSuccess(command, stdout string, exit int32) []byte {
	success := encodeString(nil, fieldShellSuccessCommand, command)
	success = encodeInt32(success, fieldShellSuccessExit, exit)
	success = encodeString(success, fieldShellSuccessStdout, stdout)
	return encodeMessage(nil, fieldResultSuccess, success)
}

func encodeShellRejected(command, reason string) []byte {
	body := encodeString(nil, 1, command)
	body = encodeString(body, 3, reason)
	return encodeMessage(nil, 4, body)
}

func encodeShellStreamRejected(reason string) []byte {
	body := encodeString(nil, 3, reason)
	return encodeMessage(nil, 5, body)
}

func encodeGrepSuccess(pattern, path, content string) []byte {
	line := encodeInt32(nil, fieldGrepLineNumber, 1)
	line = encodeString(line, fieldGrepLineContent, content)
	file := encodeString(nil, fieldGrepFilePath, firstNonEmpty(path, "."))
	file = encodeMessage(file, fieldGrepFileMatches, line)
	unionBody := encodeMessage(nil, fieldGrepContentMatches, file)
	unionBody = encodeInt32(unionBody, fieldGrepContentTotal, 1)
	unionBody = encodeInt32(unionBody, fieldGrepContentMatched, 1)
	union := encodeMessage(nil, fieldGrepUnionContent, unionBody)
	entry := encodeString(nil, 1, firstNonEmpty(path, "."))
	entry = encodeMessage(entry, 2, union)
	success := encodeString(nil, fieldGrepSuccessPattern, firstNonEmpty(pattern, "."))
	success = encodeString(success, fieldGrepSuccessPath, firstNonEmpty(path, "."))
	success = encodeString(success, fieldGrepSuccessMode, "content")
	success = encodeMessage(success, fieldGrepSuccessWorkspace, entry)
	return encodeMessage(nil, fieldResultSuccess, success)
}

func encodeGrepError(err string) []byte {
	return encodeMessage(nil, fieldResultError, encodeString(nil, 1, err))
}

func encodeLsSuccess(path, content string) []byte {
	root := encodeString(nil, fieldLsNodePath, firstNonEmpty(path, "."))
	count := int32(0)
	for _, name := range strings.Split(content, "\n") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		root = encodeMessage(root, fieldLsNodeFiles, encodeString(nil, fieldLsFileName, name))
		count++
	}
	root = encodeBool(root, fieldLsNodeProcessed, true)
	root = encodeInt32(root, fieldLsNodeNumFiles, count)
	return encodeMessage(nil, fieldResultSuccess, encodeMessage(nil, fieldLsSuccessRoot, root))
}

func encodeLsError(err string) []byte {
	return encodeMessage(nil, fieldResultError, encodeString(nil, 1, err))
}

func encodeMCPSuccess(content string, isError bool) []byte {
	text := encodeString(nil, fieldMCPTextValue, content)
	item := encodeMessage(nil, fieldMCPContentText, text)
	success := encodeMessage(nil, fieldMCPSuccessContent, item)
	if isError {
		success = encodeBool(success, fieldMCPSuccessIsError, true)
	}
	return encodeMessage(nil, fieldResultSuccess, success)
}

func encodeMCPRejected(reason string) []byte {
	return encodeMessage(nil, fieldResultRejected, encodeString(nil, 1, reason))
}

func encodeDeleteSuccess(path, content string) []byte {
	size := int64(0)
	if parsed := jsonInt64(content, "size"); parsed > 0 {
		size = parsed
	}
	deleted := firstNonEmpty(jsonString([]byte(content), "path"), path)
	success := encodeString(nil, fieldDeleteSuccessPath, path)
	success = encodeString(success, fieldDeleteSuccessFile, deleted)
	success = encodeInt64(success, fieldDeleteSuccessSize, size)
	return encodeMessage(nil, fieldResultSuccess, success)
}

func encodeDeleteFailure(path, reason, code string) []byte {
	switch code {
	case DeleteCodeNotFound:
		return encodeMessage(nil, fieldDeleteResultNotFound, encodeString(nil, fieldDeleteNotFoundPath, path))
	case DeleteCodeNotFile:
		actual := "other"
		if strings.Contains(strings.ToLower(reason), "director") {
			actual = "directory"
		}
		body := encodeString(nil, fieldDeleteNotFilePath, path)
		body = encodeString(body, fieldDeleteNotFileType, actual)
		return encodeMessage(nil, fieldDeleteResultNotFile, body)
	case DeleteCodeDenied:
		body := encodeString(nil, fieldDeleteDeniedPath, path)
		body = encodeString(body, fieldDeleteDeniedError, reason)
		return encodeMessage(nil, fieldDeleteResultDenied, body)
	case DeleteCodeBusy:
		return encodeMessage(nil, fieldDeleteResultBusy, encodeString(nil, fieldDeleteBusyPath, path))
	case DeleteCodeRejected:
		body := encodeString(nil, fieldDeleteRejectedPath, path)
		body = encodeString(body, fieldDeleteRejectedReason, reason)
		return encodeMessage(nil, fieldDeleteResultRejected, body)
	default:
		body := encodeString(nil, fieldDeleteErrorPath, path)
		body = encodeString(body, fieldDeleteErrorText, reason)
		return encodeMessage(nil, fieldDeleteResultError, body)
	}
}

func jsonInt64(raw, key string) int64 {
	var object map[string]any
	if json.Unmarshal([]byte(raw), &object) != nil {
		return 0
	}
	switch value := object[key].(type) {
	case float64:
		return int64(value)
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	default:
		return 0
	}
}

func encodePiEditSuccess(output string) []byte {
	success := encodeString(nil, fieldPiEditOut, output)
	return encodeMessage(nil, fieldResultSuccess, success)
}

func encodePiEditError(reason string) []byte {
	return encodeMessage(nil, fieldPiEditResError, encodeString(nil, fieldPiEditErr, reason))
}

func encodePiEditRejected(reason string) []byte {
	return encodeMessage(nil, fieldPiEditResReject, encodeString(nil, fieldPiEditReject, reason))
}

func encodePiFindSuccess(output string) []byte {
	success := encodeString(nil, fieldPiFindOut, output)
	return encodeMessage(nil, fieldResultSuccess, success)
}

func encodePiFindError(reason string) []byte {
	return encodeMessage(nil, fieldPiFindResError, encodeString(nil, fieldPiFindErr, reason))
}

func writeConnect(writer io.Writer, payload []byte) error {
	_, err := writer.Write(encodeConnectFrame(payload, false))
	return err
}

func jsonString(raw []byte, key string) string {
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		return ""
	}
	value, _ := object[key].(string)
	return value
}
