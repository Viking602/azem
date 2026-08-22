package cursor

import (
	"encoding/binary"
	"fmt"
)

const (
	connectFlagCompressed      = 0x01
	connectFlagEndStream       = 0x02
	maxConnectFrameBytes       = 16 << 20
	maxCursorProtoFields       = 1 << 16
	maxCursorProtoDecodedBytes = 64 << 20
	maxCursorProtoValueDepth   = 128

	fieldAgentRunRequest  = 1
	fieldAgentKVClient    = 3
	fieldAgentHeartbeat   = 7
	fieldAgentCheckpoint  = 3
	fieldAgentInteraction = 1
	fieldAgentExecServer  = 2
	fieldAgentKVServer    = 4

	fieldRunState          = 1
	fieldRunAction         = 2
	fieldRunModelDetails   = 3
	fieldRunMCPTools       = 4
	fieldRunConversationID = 5
	fieldRunRequestedModel = 9

	fieldStateRootPromptJSON        = 1
	fieldStateTurns                 = 8
	fieldStateTokenDetails          = 5
	fieldTokenUsed                  = 1
	fieldTokenMax                   = 2
	fieldStateTodos                 = 3
	fieldStatePreviousWorkspace     = 9
	fieldActionUserMessage          = 1
	fieldActionResume               = 2
	fieldUserMessageText            = 1
	fieldUserMessageID              = 2
	fieldUserMessageActionMsg       = 1
	fieldConversationTurnAgent      = 1
	fieldAgentTurnUserMessage       = 1
	fieldAgentTurnSteps             = 2
	fieldConversationStepAssistant  = 1
	fieldConversationStepTool       = 2
	fieldConversationStepThinking   = 3
	fieldAssistantMessageText       = 1
	fieldThinkingMessageText        = 1
	fieldMCPCallArgs                = 1
	fieldMCPCallResult              = 2
	fieldMCPArgsName                = 1
	fieldMCPArgsMap                 = 2
	fieldMCPArgsToolCallID          = 3
	fieldMCPArgsProvider            = 4
	fieldMCPArgsToolName            = 5
	fieldToolCallMCP                = 15
	fieldToolCallEnvelopeID         = 57
	fieldMCPToolResultSuccess       = 1
	fieldMCPToolResultError         = 2
	fieldMCPToolErrorText           = 1
	fieldMapKey                     = 1
	fieldMapValue                   = 2
	fieldValueNull                  = 1
	fieldValueNumber                = 2
	fieldValueString                = 3
	fieldValueBool                  = 4
	fieldValueStruct                = 5
	fieldValueList                  = 6
	fieldStructFields               = 1
	fieldListValueItem              = 1
	fieldUserMessageSelectedContext = 3
	fieldSelectedContextImages      = 1
	fieldSelectedImageUUID          = 2
	fieldSelectedImageMIME          = 7
	fieldSelectedImageData          = 8

	fieldModelID           = 1
	fieldModelThinking     = 2
	fieldModelDisplayID    = 3
	fieldModelDisplayName  = 4
	fieldModelDisplayShort = 5
	fieldModelAlias        = 6
	fieldModelMaxMode      = 7
	fieldRequestedModelID  = 1
	fieldRequestedMaxMode  = 2

	fieldMCPToolsItem      = 1
	fieldMCPDefName        = 1
	fieldMCPDefSchema      = 3
	fieldMCPDefDescription = 2
	fieldMCPDefProvider    = 4
	fieldMCPDefToolName    = 5
	fieldMCPDefSchemaJSON  = 6

	fieldKVID               = 1
	fieldKVGetArgs          = 2
	fieldKVGetResult        = 2
	fieldKVSetResult        = 3
	fieldKVSetArgs          = 3
	fieldBlobID             = 1
	fieldBlobData           = 1
	fieldSetBlobData        = 2
	fieldSetBlobResultError = 1
	fieldCursorErrorMessage = 1

	fieldTextDelta          = 1
	fieldToolCallStarted    = 2
	fieldThinkingDelta      = 4
	fieldTokenDelta         = 8
	fieldToolCallCompleted  = 3
	fieldTodoCallResult     = 2
	fieldTodoResultSuccess  = 1
	fieldTodoResultError    = 2
	fieldTodoSuccessItems   = 1
	fieldTodoSuccessMerged  = 3
	fieldTodoItemID         = 1
	fieldTodoItemContent    = 2
	fieldTodoItemStatus     = 3
	fieldTodoErrorText      = 1
	fieldTurnEnded          = 14
	fieldUpdateText         = 1
	fieldUpdateTokens       = 1
	fieldToolCallID         = 1
	fieldToolCallBody       = 2
	fieldToolShellCall      = 1
	fieldToolDeleteCall     = 3
	fieldToolGlobCall       = 4
	fieldToolGrepCall       = 5
	fieldToolReadCall       = 8
	fieldToolTodosCall      = 9
	fieldToolEditCall       = 12
	fieldToolLsCall         = 13
	fieldMCPToolCall        = 15
	fieldToolFetchCall      = 24
	fieldMCPArgs            = 1
	fieldMCPArgName         = 1
	fieldMCPArgMap          = 2
	fieldMCPArgToolCallID   = 3
	fieldMCPArgToolName     = 5
	fieldNestedArgs         = 1
	fieldReadPath           = 1
	fieldReadOffset         = 2
	fieldReadLimit          = 3
	fieldReadToolCallID     = 2
	fieldShellCommand       = 1
	fieldShellTimeout       = 3
	fieldShellToolCallID    = 4
	fieldWritePath          = 1
	fieldWriteText          = 2
	fieldWriteToolCallID    = 3
	fieldWriteBytes         = 5
	fieldGrepPattern        = 1
	fieldGrepPath           = 2
	fieldGrepGlob           = 3
	fieldGrepHeadLimit      = 10
	fieldGrepToolCallID     = 14
	fieldLsPath             = 1
	fieldLsToolCallID       = 3
	fieldExecMessageID      = 1
	fieldExecShellArgs      = 2
	fieldExecWriteArgs      = 3
	fieldExecDeleteArgs     = 4
	fieldExecGrepArgs       = 5
	fieldExecReadArgs       = 7
	fieldExecLsArgs         = 8
	fieldExecMCPArgs        = 11
	fieldExecShellStream    = 14
	fieldExecID             = 15
	fieldExecFetchArgs      = 20
	fieldExecPiReadArgs     = 45
	fieldExecPiBashArgs     = 46
	fieldExecPiEditArgs     = 47
	fieldExecPiWriteArgs    = 48
	fieldExecPiGrepArgs     = 49
	fieldExecPiFindArgs     = 50
	fieldExecPiLsArgs       = 51
	fieldExecRequestContext = 10

	fieldAgentExecClient        = 2
	fieldAgentExecClientControl = 5
	fieldExecClientID           = 1
	fieldExecClientExecID       = 15
	fieldExecClientShell        = 2
	fieldExecClientWrite        = 3
	fieldExecClientDelete       = 4
	fieldExecClientGrep         = 5
	fieldExecClientRead         = 7
	fieldExecClientLs           = 8
	fieldExecClientRequestCtx   = 10
	fieldExecClientMCP          = 11
	fieldExecClientShellStream  = 14
	fieldExecClientPiEdit       = 48
	fieldExecClientPiFind       = 51
	fieldExecControlClose       = 1
	fieldExecControlThrow       = 2
	fieldResultSuccess          = 1
	fieldResultError            = 2
	fieldResultRejected         = 3
	fieldRequestContextTools    = 7
	fieldRequestContextInner    = 1
	fieldReadSuccessPath        = 1
	fieldReadSuccessContent     = 2
	fieldReadSuccessLines       = 3
	fieldReadSuccessSize        = 4
	fieldReadSuccessTruncated   = 6
	fieldWriteSuccessPath       = 1
	fieldWriteSuccessLines      = 2
	fieldWriteSuccessSize       = 3
	fieldErrorPath              = 1
	fieldErrorText              = 2
	fieldRejectedPath           = 1
	fieldRejectedReason         = 2
	fieldShellSuccessCommand    = 1
	fieldShellSuccessExit       = 3
	fieldShellSuccessStdout     = 5
	fieldShellSuccessStderr     = 6
	fieldShellStreamStdout      = 1
	fieldShellStreamExit        = 3
	fieldShellStreamStart       = 4
	fieldShellStreamData        = 1
	fieldShellStreamExitCode    = 1
	fieldGrepSuccessPattern     = 1
	fieldGrepSuccessPath        = 2
	fieldGrepSuccessMode        = 3
	fieldGrepSuccessWorkspace   = 4
	fieldGrepUnionContent       = 3
	fieldGrepContentMatches     = 1
	fieldGrepContentTotal       = 2
	fieldGrepContentMatched     = 3
	fieldGrepFilePath           = 1
	fieldGrepFileMatches        = 2
	fieldGrepLineNumber         = 1
	fieldGrepLineContent        = 2
	fieldLsSuccessRoot          = 1
	fieldLsNodePath             = 1
	fieldLsNodeFiles            = 3
	fieldLsNodeProcessed        = 4
	fieldLsNodeNumFiles         = 6
	fieldLsFileName             = 1
	fieldMCPSuccessContent      = 1
	fieldMCPSuccessIsError      = 2
	fieldMCPContentText         = 1
	fieldMCPTextValue           = 1
	fieldThrowID                = 1
	fieldThrowError             = 2
	fieldCloseID                = 1
	fieldDeletePath             = 1
	fieldDeleteToolCallID       = 2
	fieldDeleteSuccessPath      = 1
	fieldDeleteSuccessFile      = 2
	fieldDeleteSuccessSize      = 3
	fieldDeleteSuccessPrev      = 4
	fieldDeleteNotFoundPath     = 1
	fieldDeleteNotFilePath      = 1
	fieldDeleteNotFileType      = 2
	fieldDeleteDeniedPath       = 1
	fieldDeleteDeniedError      = 2
	fieldDeleteDeniedReadonly   = 3
	fieldDeleteBusyPath         = 1
	fieldDeleteRejectedPath     = 1
	fieldDeleteRejectedReason   = 2
	fieldDeleteErrorPath        = 1
	fieldDeleteErrorText        = 2
	fieldDeleteResultNotFound   = 2
	fieldDeleteResultNotFile    = 3
	fieldDeleteResultDenied     = 4
	fieldDeleteResultBusy       = 5
	fieldDeleteResultRejected   = 6
	fieldDeleteResultError      = 7
	fieldPiEditPath             = 1
	fieldPiEditEdits            = 2
	fieldPiEditOldText          = 1
	fieldPiEditNewText          = 2
	fieldPiEditOut              = 1
	fieldPiEditDiff             = 2
	fieldPiEditPatch            = 3
	fieldPiEditFirstLine        = 4
	fieldPiEditErr              = 1
	fieldPiEditReject           = 1
	fieldPiEditResError         = 2
	fieldPiEditResReject        = 3
	fieldPiFindPattern          = 1
	fieldPiFindPath             = 2
	fieldPiFindLimit            = 3
	fieldPiFindOut              = 1
	fieldPiFindErr              = 1
	fieldPiFindResError         = 2
)

func encodeConnectFrame(payload []byte, end bool) []byte {
	frame := make([]byte, 5+len(payload))
	if end {
		frame[0] = connectFlagEndStream
	}
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[5:], payload)
	return frame
}

func decodeConnectFrames(payload []byte) (messages [][]byte, err error) {
	offset := 0
	for offset+5 <= len(payload) {
		flags := payload[offset]
		length := int(binary.BigEndian.Uint32(payload[offset+1 : offset+5]))
		if length > maxConnectFrameBytes {
			return nil, fmt.Errorf("cursor connect frame exceeds %d bytes", maxConnectFrameBytes)
		}
		end := offset + 5 + length
		if end > len(payload) {
			return nil, fmt.Errorf("cursor connect frame truncated")
		}
		if flags&connectFlagCompressed != 0 {
			return nil, fmt.Errorf("cursor connect frame is compressed")
		}
		if flags&connectFlagEndStream == 0 {
			messages = append(messages, append([]byte(nil), payload[offset+5:end]...))
		}
		offset = end
	}
	if offset != len(payload) && len(messages) == 0 && len(payload) > 0 {
		return [][]byte{payload}, nil
	}
	return messages, nil
}

func encodeVarint(buf []byte, value uint64) []byte {
	for value >= 0x80 {
		buf = append(buf, byte(value)|0x80)
		value >>= 7
	}
	return append(buf, byte(value))
}

func encodeKey(buf []byte, field, wire int) []byte {
	return encodeVarint(buf, uint64(field<<3|wire))
}

func encodeBytes(buf []byte, field int, value []byte) []byte {
	buf = encodeKey(buf, field, 2)
	buf = encodeVarint(buf, uint64(len(value)))
	return append(buf, value...)
}

func encodeString(buf []byte, field int, value string) []byte {
	if value == "" {
		return buf
	}
	return encodeBytes(buf, field, []byte(value))
}

func encodeBool(buf []byte, field int, value bool) []byte {
	if !value {
		return buf
	}
	buf = encodeKey(buf, field, 0)
	return append(buf, 1)
}

func encodeUint32(buf []byte, field int, value uint32) []byte {
	buf = encodeKey(buf, field, 0)
	return encodeVarint(buf, uint64(value))
}

func encodeInt32(buf []byte, field int, value int32) []byte {
	buf = encodeKey(buf, field, 0)
	return encodeVarint(buf, uint64(uint32(value)))
}

func encodeInt64(buf []byte, field int, value int64) []byte {
	buf = encodeKey(buf, field, 0)
	return encodeVarint(buf, uint64(value))
}

func encodeMessage(buf []byte, field int, message []byte) []byte {
	return encodeBytes(buf, field, message)
}

func encodeProtoField(buf []byte, field protoField) []byte {
	switch field.Wire {
	case 0:
		buf = encodeKey(buf, field.Field, field.Wire)
		return encodeVarint(buf, field.Var)
	case 1, 5:
		buf = encodeKey(buf, field.Field, field.Wire)
		return append(buf, field.Bytes...)
	case 2:
		return encodeBytes(buf, field.Field, field.Bytes)
	default:
		return buf
	}
}

type protoField struct {
	Field int
	Wire  int
	Bytes []byte
	Var   uint64
}

type protoDecodeBudget struct {
	fields       int
	decodedBytes int
}

func (budget *protoDecodeBudget) consumePayload(size int) error {
	if budget == nil {
		return fmt.Errorf("cursor proto decode budget is nil")
	}
	if size < 0 || size > maxCursorProtoDecodedBytes-budget.decodedBytes {
		return fmt.Errorf("cursor proto decoded byte budget exceeds %d", maxCursorProtoDecodedBytes)
	}
	budget.decodedBytes += size
	return nil
}

func (budget *protoDecodeBudget) consumeField() error {
	if budget == nil {
		return fmt.Errorf("cursor proto decode budget is nil")
	}
	if budget.fields >= maxCursorProtoFields {
		return fmt.Errorf("cursor proto field budget exceeds %d", maxCursorProtoFields)
	}
	budget.fields++
	return nil
}

func decodeFields(payload []byte) ([]protoField, error) {
	return decodeFieldsWithBudget(payload, &protoDecodeBudget{})
}

func decodeFieldsWithBudget(payload []byte, budget *protoDecodeBudget) ([]protoField, error) {
	if err := budget.consumePayload(len(payload)); err != nil {
		return nil, err
	}
	var fields []protoField
	offset := 0
	for offset < len(payload) {
		key, n, err := decodeVarint(payload[offset:])
		if err != nil {
			return nil, err
		}
		offset += n
		field := int(key >> 3)
		wire := int(key & 7)
		if err := budget.consumeField(); err != nil {
			return nil, err
		}
		current := protoField{Field: field, Wire: wire}
		switch wire {
		case 0:
			value, size, err := decodeVarint(payload[offset:])
			if err != nil {
				return nil, err
			}
			current.Var = value
			offset += size
		case 1:
			if offset+8 > len(payload) {
				return nil, fmt.Errorf("cursor proto fixed64 truncated")
			}
			current.Bytes = payload[offset : offset+8]
			offset += 8
		case 2:
			length, size, err := decodeVarint(payload[offset:])
			if err != nil {
				return nil, err
			}
			offset += size
			if length > uint64(len(payload)-offset) {
				return nil, fmt.Errorf("cursor proto bytes truncated")
			}
			end := offset + int(length)
			current.Bytes = payload[offset:end]
			offset = end
		case 5:
			if offset+4 > len(payload) {
				return nil, fmt.Errorf("cursor proto fixed32 truncated")
			}
			current.Bytes = payload[offset : offset+4]
			offset += 4
		default:
			return nil, fmt.Errorf("cursor proto unsupported wire type %d", wire)
		}
		fields = append(fields, current)
	}
	return fields, nil
}

func decodeVarint(payload []byte) (uint64, int, error) {
	var value uint64
	for index, item := range payload {
		if index == 9 && item > 1 {
			return 0, 0, fmt.Errorf("cursor proto varint overflow")
		}
		value |= uint64(item&0x7f) << (index * 7)
		if item < 0x80 {
			return value, index + 1, nil
		}
		if index == 9 {
			return 0, 0, fmt.Errorf("cursor proto varint overflow")
		}
	}
	return 0, 0, fmt.Errorf("cursor proto varint truncated")
}

func fieldString(fields []protoField, number int) string {
	for _, field := range fields {
		if field.Field == number && field.Wire == 2 {
			return string(field.Bytes)
		}
	}
	return ""
}

func fieldBytes(fields []protoField, number int) []byte {
	for _, field := range fields {
		if field.Field == number && field.Wire == 2 {
			return field.Bytes
		}
	}
	return nil
}

func fieldAllBytes(fields []protoField, number int) [][]byte {
	var values [][]byte
	for _, field := range fields {
		if field.Field == number && field.Wire == 2 {
			values = append(values, field.Bytes)
		}
	}
	return values
}

func fieldUint32(fields []protoField, number int) uint32 {
	for _, field := range fields {
		if field.Field == number && field.Wire == 0 {
			return uint32(field.Var)
		}
	}
	return 0
}

func fieldRepeated(fields []protoField, number int) [][]byte {
	var values [][]byte
	for _, field := range fields {
		if field.Field == number && field.Wire == 2 {
			values = append(values, field.Bytes)
		}
	}
	return values
}
