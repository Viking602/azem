package session

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

const artifactPreviewVersion = 2

type ArtifactPreviewSnippet struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

type ArtifactPreviewV2 struct {
	Version   int                      `json:"version"`
	Kind      string                   `json:"kind"`
	Bytes     int                      `json:"bytes"`
	Lines     int                      `json:"lines"`
	SHA256    string                   `json:"sha256"`
	Encoding  string                   `json:"encoding"`
	Head      string                   `json:"head"`
	Tail      string                   `json:"tail"`
	Errors    []ArtifactPreviewSnippet `json:"errors"`
	Warnings  []ArtifactPreviewSnippet `json:"warnings"`
	Hint      string                   `json:"hint,omitempty"`
	Truncated bool                     `json:"truncated"`
}

func BuildArtifactPreviewV2(kind string, payload []byte, sha256, hint string) string {
	const excerptBytes = 2048
	preview := ArtifactPreviewV2{
		Version: artifactPreviewVersion, Kind: kind, Bytes: len(payload), Lines: artifactLineCount(payload),
		SHA256: sha256, Encoding: "utf-8", Truncated: len(payload) > excerptBytes*2,
		Hint: strings.ToValidUTF8(strings.TrimSpace(truncateBytes(hint, 1024)), "�"),
	}
	head, tail := payload, payload
	if len(head) > excerptBytes {
		head = head[:excerptBytes]
	}
	if len(tail) > excerptBytes {
		tail = tail[len(tail)-excerptBytes:]
	}
	if utf8.Valid(payload) {
		preview.Head = strings.ToValidUTF8(string(head), "�")
		preview.Tail = strings.ToValidUTF8(string(tail), "�")
		preview.Errors, preview.Warnings = artifactDiagnosticSnippets(payload)
	} else {
		preview.Encoding = "base64"
		preview.Head = base64.StdEncoding.EncodeToString(head)
		preview.Tail = base64.StdEncoding.EncodeToString(tail)
	}
	encoded, _ := json.Marshal(preview)
	return string(encoded)
}

func artifactLineCount(payload []byte) int {
	if len(payload) == 0 {
		return 0
	}
	count := bytes.Count(payload, []byte{'\n'})
	if payload[len(payload)-1] != '\n' {
		count++
	}
	return count
}

func artifactDiagnosticSnippets(payload []byte) (errorsFound, warningsFound []ArtifactPreviewSnippet) {
	const maxSnippets = 8
	line, offset := 1, 0
	for offset < len(payload) && !artifactSnippetLimitReached(errorsFound, warningsFound, maxSnippets) {
		end := bytes.IndexByte(payload[offset:], '\n')
		if end < 0 {
			end = len(payload) - offset
		}
		value := strings.TrimSpace(strings.ToValidUTF8(string(payload[offset:offset+end]), "�"))
		snippet := ArtifactPreviewSnippet{Line: line, Text: truncateBytes(value, 1024)}
		switch artifactSnippetKind(value) {
		case "error":
			if len(errorsFound) < maxSnippets {
				errorsFound = append(errorsFound, snippet)
			}
		case "warning":
			if len(warningsFound) < maxSnippets {
				warningsFound = append(warningsFound, snippet)
			}
		}
		offset += end + 1
		line++
	}
	return errorsFound, warningsFound
}

func artifactSnippetLimitReached(errorsFound, warningsFound []ArtifactPreviewSnippet, limit int) bool {
	return len(errorsFound) >= limit && len(warningsFound) >= limit
}

func artifactSnippetKind(value string) string {
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	for _, marker := range []string{"error", "fatal", "panic", "failed"} {
		if strings.Contains(lower, marker) {
			return "error"
		}
	}
	for _, marker := range []string{"warn", "deprecated"} {
		if strings.Contains(lower, marker) {
			return "warning"
		}
	}
	return ""
}

func truncateBytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
