package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	hyagent "github.com/Viking602/venat/agent"
	"github.com/Viking602/venat/message"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	maxSubagentOutputSchemaBytes = 64 << 10
	structuredSubagentRetries    = 2
)

type structuredSubagentContract struct {
	raw    json.RawMessage
	mode   string
	schema *jsonschema.Schema
}

type structuredSubagentResult struct {
	Source string          `json:"source"`
	Mode   string          `json:"mode"`
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func compileStructuredSubagentContract(raw json.RawMessage, mode string) (*structuredSubagentContract, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if len(raw) > maxSubagentOutputSchemaBytes {
		return nil, fmt.Errorf("outputSchema exceeds %d bytes", maxSubagentOutputSchemaBytes)
	}
	if mode == "" {
		mode = "permissive"
	}
	if mode != "permissive" && mode != "strict" {
		return nil, fmt.Errorf("schemaMode must be permissive or strict")
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("outputSchema is invalid JSON: %w", err)
	}
	if encoded, ok := document.(string); ok {
		if err := json.Unmarshal([]byte(encoded), &document); err != nil {
			return nil, fmt.Errorf("outputSchema string is invalid JSON: %w", err)
		}
	}
	switch document.(type) {
	case map[string]any, bool:
	default:
		return nil, errors.New("outputSchema must be a JSON Schema object, boolean, JSON string, or null")
	}
	normalized, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("normalize outputSchema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	const location = "https://azem.local/subagent-output.schema.json"
	if err := compiler.AddResource(location, document); err != nil {
		return nil, fmt.Errorf("register outputSchema: %w", err)
	}
	compiled, err := compiler.Compile(location)
	if err != nil {
		return nil, fmt.Errorf("compile outputSchema: %w", err)
	}
	return &structuredSubagentContract{raw: normalized, mode: mode, schema: compiled}, nil
}

func (contract *structuredSubagentContract) instruction() string {
	if contract == nil {
		return ""
	}
	return "Structured completion contract:\nReturn only one JSON value matching this JSON Schema. Do not wrap it in Markdown or add prose.\n" + string(contract.raw)
}

func (contract *structuredSubagentContract) validate(text string) structuredSubagentResult {
	result := structuredSubagentResult{Source: "caller", Mode: contract.mode, Status: "invalid"}
	payload, value, err := parseStructuredSubagentJSON(text)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Data = payload
	if err := contract.schema.Validate(value); err != nil {
		result.Error = err.Error()
		return result
	}
	result.Status = "valid"
	return result
}

func structuredSubagentGuardrail(contract *structuredSubagentContract) hyagent.OutputGuardrail {
	if contract == nil {
		return nil
	}
	attempts := 0
	return hyagent.NewOutputGuardrail("structured-subagent-output", func(_ context.Context, input hyagent.OutputGuardrailInput) (hyagent.OutputGuardrailResult, error) {
		result := contract.validate(input.Output.Text)
		if result.Status == "valid" {
			return hyagent.AllowOutput(), nil
		}
		attempts++
		if attempts <= structuredSubagentRetries {
			return hyagent.RetryOutputWithPolicy(hyagent.RetryPolicy{IncludeRejectedOutput: true}, message.NewText(message.RoleUser, "Your final output did not match the required JSON Schema: "+result.Error+"\\nReturn only one corrected JSON value, with no Markdown fence or prose.")), nil
		}
		if contract.mode == "strict" {
			return hyagent.BlockOutput("schema_violation: " + result.Error), nil
		}
		return hyagent.AllowOutput(), nil
	})
}

func parseStructuredSubagentJSON(text string) (json.RawMessage, any, error) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") && strings.HasSuffix(text, "```") {
		body := strings.TrimSuffix(strings.TrimPrefix(text, "```"), "```")
		body = strings.TrimSpace(body)
		if first, rest, found := strings.Cut(body, "\n"); found && (strings.EqualFold(strings.TrimSpace(first), "json") || strings.TrimSpace(first) == "") {
			body = strings.TrimSpace(rest)
		}
		text = body
	}
	if text == "" {
		return nil, nil, errors.New("structured output is empty")
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, nil, fmt.Errorf("structured output is invalid JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, nil, errors.New("structured output contains more than one JSON value")
		}
		return nil, nil, fmt.Errorf("structured output has trailing data: %w", err)
	}
	compact, err := json.Marshal(value)
	if err != nil {
		return nil, nil, fmt.Errorf("normalize structured output: %w", err)
	}
	return compact, value, nil
}
