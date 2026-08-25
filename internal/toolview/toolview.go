// Package toolview is the single source of truth for projecting file-change
// tool activity into structured UI summaries. The desktop frontend and the
// TUI previously parsed hashline/write-file payloads independently; both now
// consume this projection (live via the tool_finished event `fileChange`
// payload and durable replay via the session tool records), so a semantic
// change happens in exactly one place.
package toolview

import (
	"encoding/json"
	"strconv"
	"strings"
)

// FileChange is one changed file inside a completed file-change tool call.
// Diff holds compact +/- lines (not a unified diff); Additions/Deletions are
// derived from it. An empty Diff is preserved (for example an empty file
// write) so renderers can choose their own placeholder.
type FileChange struct {
	Path             string `json:"path"`
	FirstChangedLine int    `json:"firstChangedLine"`
	Diff             string `json:"diff"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
}

// FileChangeSummary aggregates every file changed by one tool call.
type FileChangeSummary struct {
	Files     []FileChange `json:"files"`
	Additions int          `json:"additions"`
	Deletions int          `json:"deletions"`
}

// IsFileChangeTool reports whether the named tool mutates workspace files and
// therefore participates in file-change projections.
func IsFileChangeTool(name string) bool {
	return name == "coding.edit_hashline" || name == "coding.replace" ||
		name == "coding.write_file" || name == "coding.delete_file"
}

// CompletedFileChanges projects a completed file-change tool call into its
// structured summary. Hashline/replace edits prefer structured result sections
// and fall back to compact output; writes derive content from arguments; deletes
// retain their target path. It returns false when the tool is not a file-change
// tool or nothing attributable was found.
func CompletedFileChanges(name, arguments, structured, output string) (FileChangeSummary, bool) {
	var sections []FileChange
	switch name {
	case "coding.edit_hashline", "coding.replace":
		sections = structuredSections(structured)
		if len(sections) == 0 {
			sections = ParseCompactEditOutput(output)
		}
	case "coding.write_file":
		sections = writeFileSections(arguments)
	case "coding.delete_file":
		sections = pathOnlySections(arguments)
	default:
		return FileChangeSummary{}, false
	}
	summary := summarize(sections)
	return summary, len(summary.Files) > 0
}

// EncodeSummary renders the summary as the canonical JSON wire form used in
// event payloads and durable tool-record projections.
func EncodeSummary(summary FileChangeSummary) string {
	encoded, err := json.Marshal(summary)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// DecodeSummary parses the canonical wire form; ok is false for empty or
// malformed payloads.
func DecodeSummary(value string) (FileChangeSummary, bool) {
	if strings.TrimSpace(value) == "" {
		return FileChangeSummary{}, false
	}
	var summary FileChangeSummary
	if err := json.Unmarshal([]byte(value), &summary); err != nil {
		return FileChangeSummary{}, false
	}
	return summary, len(summary.Files) > 0
}

func summarize(sections []FileChange) FileChangeSummary {
	summary := FileChangeSummary{}
	for _, section := range sections {
		path := strings.TrimSpace(section.Path)
		if path == "" {
			continue
		}
		diff := normalizeNewlines(section.Diff)
		additions, deletions := countChanges(diff)
		firstChangedLine := section.FirstChangedLine
		if firstChangedLine < 1 {
			firstChangedLine = 1
		}
		summary.Files = append(summary.Files, FileChange{
			Path: path, FirstChangedLine: firstChangedLine, Diff: diff,
			Additions: additions, Deletions: deletions,
		})
		summary.Additions += additions
		summary.Deletions += deletions
	}
	return summary
}

func structuredSections(structured string) []FileChange {
	if strings.TrimSpace(structured) == "" {
		return nil
	}
	var result struct {
		Sections []FileChange `json:"sections"`
	}
	if err := json.Unmarshal([]byte(structured), &result); err != nil {
		return nil
	}
	return result.Sections
}

func writeFileSections(arguments string) []FileChange {
	var input struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return nil
	}
	path := strings.TrimSpace(input.Path)
	if path == "" {
		return nil
	}
	content := normalizeNewlines(input.Content)
	var lines []string
	if content != "" {
		lines = strings.Split(strings.TrimSuffix(content, "\n"), "\n")
		for index := range lines {
			lines[index] = "+" + lines[index]
		}
	}
	return []FileChange{{Path: path, FirstChangedLine: 1, Diff: strings.Join(lines, "\n")}}
}

func pathOnlySections(arguments string) []FileChange {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return nil
	}
	path := strings.TrimSpace(input.Path)
	if path == "" {
		return nil
	}
	return []FileChange{{Path: path, FirstChangedLine: 1}}
}

// ParseCompactEditOutput recovers current `[path#TAG]` sections and legacy
// `¶path#TAG` sections from durable compact edit output when structured data is absent.
func ParseCompactEditOutput(output string) []FileChange {
	var sections []FileChange
	var current *FileChange
	var diffLines []string
	inDiff := false
	flush := func() {
		if current == nil {
			return
		}
		current.Diff = strings.Trim(strings.Join(diffLines, "\n"), "\n")
		if current.Path != "" && current.Diff != "" {
			sections = append(sections, *current)
		}
	}
	for _, line := range strings.Split(normalizeNewlines(output), "\n") {
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			flush()
			inner := strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			if marker := strings.LastIndex(inner, "#"); marker > 0 {
				current = &FileChange{Path: strings.TrimSpace(inner[:marker]), FirstChangedLine: 1}
				diffLines = nil
				inDiff = false
			}
			continue
		}
		if strings.HasPrefix(line, "¶") {
			flush()
			path := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(line, "#", 2)[0], "¶"))
			current = &FileChange{Path: path, FirstChangedLine: 1}
			diffLines = nil
			inDiff = false
			continue
		}
		if current == nil {
			continue
		}
		if value, ok := strings.CutPrefix(line, "firstChangedLine: "); ok {
			if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && parsed > 0 {
				current.FirstChangedLine = parsed
			}
			continue
		}
		if line == "--- compact diff ---" {
			inDiff = true
			continue
		}
		if inDiff {
			diffLines = append(diffLines, line)
		}
	}
	flush()
	return sections
}

func countChanges(diff string) (additions, deletions int) {
	if diff == "" {
		return 0, 0
	}
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			additions++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			deletions++
		}
	}
	return additions, deletions
}

func normalizeNewlines(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}
