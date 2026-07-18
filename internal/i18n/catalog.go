// Package i18n provides the embedded user-interface message catalogs.
package i18n

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
)

const DefaultLanguage = "en"

//go:embed locales/*.json
var localeFiles embed.FS

var placeholders = regexp.MustCompile(`\{[A-Za-z][A-Za-z0-9_]*\}`)

type Catalog struct {
	language string
	messages map[string]map[string]string
}

func New(language string) (Catalog, error) {
	messages, err := load(localeFiles)
	if err != nil {
		return Catalog{}, err
	}
	if _, ok := messages[language]; !ok {
		return Catalog{}, fmt.Errorf("unsupported language %q", language)
	}
	return Catalog{language: language, messages: messages}, nil
}

func Must(language string) Catalog {
	c, err := New(language)
	if err != nil {
		panic(err)
	}
	return c
}

// Languages returns the locale names discovered from the embedded JSON files.
func Languages() []string {
	messages, err := load(localeFiles)
	if err != nil {
		panic(err)
	}
	languages := make([]string, 0, len(messages))
	for language := range messages {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	return languages
}

func (c Catalog) Language() string { return c.language }

func (c Catalog) T(key string, args ...map[string]string) string {
	text := c.messages[c.language][key]
	if text == "" {
		text = c.messages[DefaultLanguage][key]
	}
	if text == "" {
		return "[missing: " + key + "]"
	}
	if len(args) > 0 {
		for name, value := range args[0] {
			text = strings.ReplaceAll(text, "{"+name+"}", value)
		}
	}
	return text
}

func load(fsys fs.FS) (map[string]map[string]string, error) {
	entries, err := fs.ReadDir(fsys, "locales")
	if err != nil {
		return nil, err
	}
	all := make(map[string]map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := fs.ReadFile(fsys, "locales/"+entry.Name())
		if err != nil {
			return nil, err
		}
		values, err := decodeStrict(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		all[strings.TrimSuffix(entry.Name(), ".json")] = values
	}
	base, ok := all[DefaultLanguage]
	if !ok {
		return nil, fmt.Errorf("missing %s catalog", DefaultLanguage)
	}
	for language, values := range all {
		if len(values) != len(base) {
			return nil, fmt.Errorf("%s key set differs from %s", language, DefaultLanguage)
		}
		for key, english := range base {
			translated, ok := values[key]
			if !ok {
				return nil, fmt.Errorf("%s missing key %q", language, key)
			}
			if !sameStrings(placeholders.FindAllString(english, -1), placeholders.FindAllString(translated, -1)) {
				return nil, fmt.Errorf("%s placeholders differ for %q", language, key)
			}
		}
	}
	return all, nil
}

func decodeStrict(data []byte) (map[string]string, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("catalog must be a JSON object")
	}
	result := make(map[string]string)
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok || key == "" {
			return nil, fmt.Errorf("empty or invalid key")
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("duplicate key %q", key)
		}
		var value any
		if err := d.Decode(&value); err != nil {
			return nil, err
		}
		text, ok := value.(string)
		if !ok || text == "" {
			return nil, fmt.Errorf("%q must have a non-empty string value", key)
		}
		result[key] = text
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return nil, fmt.Errorf("invalid object ending")
	}
	if d.More() {
		return nil, fmt.Errorf("trailing JSON value")
	}
	var extra any
	if err := d.Decode(&extra); err == nil {
		return nil, fmt.Errorf("trailing JSON value")
	}
	return result, nil
}

func sameStrings(a, b []string) bool {
	sort.Strings(a)
	sort.Strings(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
