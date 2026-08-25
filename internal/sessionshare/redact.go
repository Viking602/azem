package sessionshare

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/message"
)

var commonSecretPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+)[^\s]+`), `${1}[REDACTED]`},
	{regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|refresh[_-]?token|password|secret)\s*[:=]\s*[^\s,;]+`), `${1}=[REDACTED]`},
	{regexp.MustCompile(`\b(?:sk|ghp|github_pat|xox[baprs])[-_][A-Za-z0-9_-]{12,}\b`), `[REDACTED]`},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`), `[REDACTED]`},
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), `[REDACTED PRIVATE KEY]`},
}

var secretKeyPattern = regexp.MustCompile(`(?i)(authorization|api.?key|access.?token|refresh.?token|password|secret|credential|cookie)`)

type textRedactor struct {
	secrets []string
}

func newTextRedactor(secrets []string) textRedactor {
	unique := make(map[string]struct{})
	for _, value := range secrets {
		value = strings.TrimSpace(value)
		if utf8.RuneCountInString(value) >= 4 {
			unique[value] = struct{}{}
		}
	}
	values := make([]string, 0, len(unique))
	for value := range unique {
		values = append(values, value)
	}
	sort.Slice(values, func(left, right int) bool { return len(values[left]) > len(values[right]) })
	return textRedactor{secrets: values}
}

func (redactor textRedactor) text(value string) string {
	for _, secret := range redactor.secrets {
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	for _, rule := range commonSecretPatterns {
		value = rule.pattern.ReplaceAllString(value, rule.replacement)
	}
	return value
}

func redactDocument(document Document, secrets []string) Document {
	encoded, _ := json.Marshal(document)
	var clone Document
	_ = json.Unmarshal(encoded, &clone)
	redactor := newTextRedactor(secrets)
	clone.Session.Metadata.Title = redactor.text(clone.Session.Metadata.Title)
	clone.Session.Metadata.Workspace = ""
	clone.Session.Tree.PromptCacheKey = ""
	clone.Session.Tree.SourceRef = ""
	clone.Session.Tree.ParentSessionID = redactor.text(clone.Session.Tree.ParentSessionID)
	clone.Session.Tree.ForkedFromEntryID = redactor.text(clone.Session.Tree.ForkedFromEntryID)
	clone.Session.Tree.Roots = redactTreeNodes(clone.Session.Tree.Roots, redactor)
	for index := range clone.Session.Tree.Branches {
		clone.Session.Tree.Branches[index].Name = redactor.text(clone.Session.Tree.Branches[index].Name)
	}
	for index := range clone.Session.Blocks {
		block := &clone.Session.Blocks[index]
		block.Title = redactor.text(block.Title)
		block.Content = redactor.text(block.Content)
		for key, value := range block.Data {
			if secretKeyPattern.MatchString(key) {
				block.Data[key] = "[REDACTED]"
			} else {
				block.Data[key] = redactor.text(value)
			}
		}
		for attachmentIndex := range block.Attachments {
			attachment := &block.Attachments[attachmentIndex]
			attachment.Name = redactor.text(attachment.Name)
			attachment.Path = ""
		}
		block.ImportedMessage = redactImportedMessage(block.ImportedMessage, redactor)
	}
	for index := range clone.Session.ToolRecords {
		record := &clone.Session.ToolRecords[index]
		record.SessionID = ""
		record.Content = redactor.text(record.Content)
		record.Arguments = redactJSON(record.Arguments, redactor)
		record.Structured = redactJSON(record.Structured, redactor)
		record.ArtifactID = ""
		for observationIndex := range record.Observations {
			record.Observations[observationIndex].Path = redactor.text(record.Observations[observationIndex].Path)
		}
	}
	return clone
}

func redactTreeNodes(nodes []session.TreeNode, redactor textRedactor) []session.TreeNode {
	for index := range nodes {
		nodes[index].Entry.SourceID = redactor.text(nodes[index].Entry.SourceID)
		nodes[index].Entry.Label = redactor.text(nodes[index].Entry.Label)
		nodes[index].Children = redactTreeNodes(nodes[index].Children, redactor)
	}
	return nodes
}

func redactImportedMessage(raw json.RawMessage, redactor textRedactor) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var value message.Message
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	value.Text = redactor.text(value.Text)
	value.Thinking = redactor.text(value.Thinking)
	value.ThinkingSignature = ""
	value.RedactedThinking = ""
	value.ProviderState = nil
	value.Response.Headers = nil
	for index := range value.Content {
		part := &value.Content[index]
		switch part.Kind {
		case message.ContentText, message.ContentCommentary, message.ContentFinalAnswer, message.ContentReasoning:
			part.Text = redactor.text(part.Text)
			part.Signature = ""
		case message.ContentImage, message.ContentAudio, message.ContentFile:
			part.Data = nil
			part.URI = ""
			part.Text = "[binary content omitted from share]"
		case message.ContentProviderData, message.ContentRedactedReasoning:
			part.Data = nil
			part.ProviderData = nil
			part.Text = ""
		}
		if part.Source != nil {
			part.Source.URL = redactor.text(part.Source.URL)
			part.Source.Title = redactor.text(part.Source.Title)
			part.Source.Filename = redactor.text(part.Source.Filename)
		}
	}
	for index := range value.ToolCalls {
		value.ToolCalls[index].Arguments = redactJSON(value.ToolCalls[index].Arguments, redactor)
	}
	if value.ToolResult != nil {
		value.ToolResult.Content = redactor.text(value.ToolResult.Content)
		value.ToolResult.Structured = redactJSON(value.ToolResult.Structured, redactor)
		for index := range value.ToolResult.Parts {
			part := &value.ToolResult.Parts[index]
			part.Text = redactor.text(part.Text)
			if part.Kind != message.ContentText {
				part.Data = nil
				part.ProviderData = nil
			}
		}
	}
	for key, current := range value.Metadata {
		if secretKeyPattern.MatchString(key) {
			value.Metadata[key] = "[REDACTED]"
		} else {
			value.Metadata[key] = redactor.text(current)
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}

func redactJSON(raw json.RawMessage, redactor textRedactor) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil
	}
	value = redactJSONValue(value, redactor)
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}

func redactJSONValue(value any, redactor textRedactor) any {
	switch current := value.(type) {
	case string:
		return redactor.text(current)
	case []any:
		for index := range current {
			current[index] = redactJSONValue(current[index], redactor)
		}
		return current
	case map[string]any:
		for key, item := range current {
			if secretKeyPattern.MatchString(key) {
				current[key] = "[REDACTED]"
			} else {
				current[key] = redactJSONValue(item, redactor)
			}
		}
		return current
	default:
		return value
	}
}

func fitAndSeal(document Document, key []byte, limit int) ([]byte, bool, error) {
	sealed, err := sealDocument(document, key)
	if err != nil {
		return nil, false, err
	}
	if len(sealed) <= limit {
		return sealed, false, nil
	}
	truncated := true
	stripOpaqueSharePayloads(&document)
	sealed, err = sealDocument(document, key)
	if err == nil && len(sealed) <= limit {
		return sealed, truncated, nil
	}
	for _, capRunes := range []int{32_768, 8_192, 2_048, 512} {
		capShareStrings(&document, capRunes)
		sealed, err = sealDocument(document, key)
		if err == nil && len(sealed) <= limit {
			return sealed, truncated, nil
		}
	}
	document.Session.Tree.Roots = nil
	for len(document.Session.ToolRecords) > 0 {
		document.Session.ToolRecords = document.Session.ToolRecords[1:]
		sealed, err = sealDocument(document, key)
		if err == nil && len(sealed) <= limit {
			return sealed, truncated, nil
		}
	}
	for len(document.Session.Blocks) > 0 {
		document.Session.Blocks = document.Session.Blocks[1:]
		sealed, err = sealDocument(document, key)
		if err == nil && len(sealed) <= limit {
			return sealed, truncated, nil
		}
	}
	if err != nil {
		return nil, false, err
	}
	return nil, false, fmt.Errorf("encrypted share cannot fit within %d bytes", limit)
}

func stripOpaqueSharePayloads(document *Document) {
	for index := range document.Session.Blocks {
		document.Session.Blocks[index].ImportedMessage = nil
		for attachmentIndex := range document.Session.Blocks[index].Attachments {
			document.Session.Blocks[index].Attachments[attachmentIndex].Path = ""
		}
	}
	for index := range document.Session.ToolRecords {
		document.Session.ToolRecords[index].Structured = nil
		document.Session.ToolRecords[index].Observations = nil
		document.Session.ToolRecords[index].ArtifactID = ""
	}
}

func capShareStrings(document *Document, limit int) {
	document.Session.Metadata.Title = capString(document.Session.Metadata.Title, limit)
	document.Session.Metadata.Workspace = capString(document.Session.Metadata.Workspace, limit)
	document.Session.Tree.SourceRef = capString(document.Session.Tree.SourceRef, limit)
	for index := range document.Session.Blocks {
		block := &document.Session.Blocks[index]
		block.Title = capString(block.Title, limit)
		block.Content = capString(block.Content, limit)
		for key, value := range block.Data {
			block.Data[key] = capString(value, limit)
		}
	}
	for index := range document.Session.ToolRecords {
		record := &document.Session.ToolRecords[index]
		record.Content = capString(record.Content, limit)
		if len(record.Arguments) > limit*4 {
			record.Arguments = json.RawMessage(`{"truncated":true}`)
		}
		if len(record.Structured) > limit*4 {
			record.Structured = json.RawMessage(`{"truncated":true}`)
		}
	}
}

func capString(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "\n[truncated for share]"
}
