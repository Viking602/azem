package securityscan

import (
	"embed"
	"encoding/json"
	"fmt"
	"github.com/dlclark/regexp2"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

//go:embed schemas/*.schema.json
var schemaFiles embed.FS

type ContractValidator struct {
	manifest *jsonschema.Schema
	findings *jsonschema.Schema
	coverage *jsonschema.Schema
}

var (
	defaultValidator     *ContractValidator
	defaultValidatorErr  error
	defaultValidatorOnce sync.Once
)

func DefaultValidator() (*ContractValidator, error) {
	defaultValidatorOnce.Do(func() {
		defaultValidator, defaultValidatorErr = NewContractValidator()
	})
	return defaultValidator, defaultValidatorErr
}

type schemaRegexp struct {
	value *regexp2.Regexp
}

func (r schemaRegexp) MatchString(value string) bool {
	matched, _ := r.value.MatchString(value)
	return matched
}

func (r schemaRegexp) String() string {
	return r.value.String()
}

func NewContractValidator() (*ContractValidator, error) {
	compiler := jsonschema.NewCompiler()
	compiler.UseRegexpEngine(func(pattern string) (jsonschema.Regexp, error) {
		compiled, err := regexp2.Compile(pattern, regexp2.ECMAScript)
		if err != nil {
			return nil, err
		}
		return schemaRegexp{value: compiled}, nil
	})
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiled := make(map[string]*jsonschema.Schema, 3)
	for _, name := range []string{"scan-manifest", "findings", "coverage"} {
		payload, err := schemaFiles.ReadFile("schemas/" + name + ".schema.json")
		if err != nil {
			return nil, fmt.Errorf("security scan: read %s schema: %w", name, err)
		}
		var document any
		if err := json.Unmarshal(payload, &document); err != nil {
			return nil, fmt.Errorf("security scan: decode %s schema: %w", name, err)
		}
		location := "https://azem.local/security-schema/" + name + ".json"
		if err := compiler.AddResource(location, document); err != nil {
			return nil, fmt.Errorf("security scan: register %s schema: %w", name, err)
		}
		schema, err := compiler.Compile(location)
		if err != nil {
			return nil, fmt.Errorf("security scan: compile %s schema: %w", name, err)
		}
		compiled[name] = schema
	}
	return &ContractValidator{manifest: compiled["scan-manifest"], findings: compiled["findings"], coverage: compiled["coverage"]}, nil
}

func schemaValue(value any) (any, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func validateSchema(name string, schema *jsonschema.Schema, value any) error {
	decoded, err := schemaValue(value)
	if err != nil {
		return fmt.Errorf("security scan: encode %s contract: %w", name, err)
	}
	if err := schema.Validate(decoded); err != nil {
		return fmt.Errorf("security scan: %s contract is invalid: %w", name, err)
	}
	return nil
}

func (v *ContractValidator) ValidateManifest(value any) error {
	return validateSchema("manifest", v.manifest, value)
}

func (v *ContractValidator) ValidateFindings(value any) error {
	return validateSchema("findings", v.findings, value)
}

func (v *ContractValidator) ValidateCoverage(value any) error {
	return validateSchema("coverage", v.coverage, value)
}

func SafeRelativePath(value string, allowDot bool) (string, error) {
	if value == "." && allowDot {
		return value, nil
	}
	if value == "" || !utf8.ValidString(value) || strings.ContainsAny(value, "\\\x00") || strings.HasPrefix(value, "/") || filepath.IsAbs(value) {
		return "", fmt.Errorf("security scan: unsafe relative path %q", value)
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.Contains(part, ":") {
			return "", fmt.Errorf("security scan: unsafe relative path %q", value)
		}
	}
	normalized := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if normalized == "." || strings.HasPrefix(normalized, "../") {
		return "", fmt.Errorf("security scan: unsafe relative path %q", value)
	}
	return normalized, nil
}
