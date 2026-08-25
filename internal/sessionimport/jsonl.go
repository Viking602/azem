package sessionimport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	maxSessionFileBytes = 256 << 20
	maxJSONLineBytes    = 32 << 20
	maxJSONRecords      = 1_000_000
	maxJSONDepth        = 64
)

type jsonRecord struct {
	Line  int
	Value map[string]any
}

func readJSONL(ctx context.Context, path string) ([]jsonRecord, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, nil, errors.New("foreign session source must be a regular file")
	}
	if info.Size() > maxSessionFileBytes {
		return nil, nil, fmt.Errorf("foreign session source exceeds %d bytes", maxSessionFileBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, maxSessionFileBytes+1))
	scanner.Buffer(make([]byte, 64<<10), maxJSONLineBytes)
	records := make([]jsonRecord, 0)
	line := 0
	for scanner.Scan() {
		line++
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		trimmed := bytes.TrimSpace(scanner.Bytes())
		if len(trimmed) == 0 {
			continue
		}
		value, err := decodeJSONObject(trimmed)
		if err != nil {
			return nil, nil, fmt.Errorf("foreign session line %d: %w", line, err)
		}
		records = append(records, jsonRecord{Line: line, Value: value})
		if len(records) > maxJSONRecords {
			return nil, nil, fmt.Errorf("foreign session exceeds %d records", maxJSONRecords)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("read foreign session: %w", err)
	}
	return records, info, nil
}

func readFirstJSONL(ctx context.Context, path string) (jsonRecord, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return jsonRecord{}, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return jsonRecord{}, nil, errors.New("foreign session source must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return jsonRecord{}, nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, maxJSONLineBytes+1))
	scanner.Buffer(make([]byte, 64<<10), maxJSONLineBytes)
	line := 0
	for scanner.Scan() {
		line++
		if err := ctx.Err(); err != nil {
			return jsonRecord{}, nil, err
		}
		trimmed := bytes.TrimSpace(scanner.Bytes())
		if len(trimmed) == 0 {
			continue
		}
		value, err := decodeJSONObject(trimmed)
		if err != nil {
			return jsonRecord{}, nil, fmt.Errorf("foreign session line %d: %w", line, err)
		}
		return jsonRecord{Line: line, Value: value}, info, nil
	}
	if err := scanner.Err(); err != nil {
		return jsonRecord{}, nil, err
	}
	return jsonRecord{}, info, io.EOF
}

func decodeJSONObject(payload []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	value, err := decodeUniqueValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected trailing JSON token %v", token)
		}
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("JSON record must be an object")
	}
	return object, nil
}

func decodeUniqueValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > maxJSONDepth {
		return nil, fmt.Errorf("JSON nesting exceeds %d levels", maxJSONDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("JSON object key is not a string")
			}
			if _, duplicate := object[key]; duplicate {
				return nil, fmt.Errorf("duplicate JSON object key %q", key)
			}
			value, err := decodeUniqueValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
			return nil, errors.New("unterminated JSON object")
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := decodeUniqueValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
			return nil, errors.New("unterminated JSON array")
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func objectValue(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func arrayValue(value any) []any {
	array, _ := value.([]any)
	return array
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func numberValue(value any) float64 {
	switch current := value.(type) {
	case json.Number:
		result, _ := current.Float64()
		return result
	case float64:
		return current
	default:
		return 0
	}
}

func cleanText(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(value, "\x00", ""))
}
