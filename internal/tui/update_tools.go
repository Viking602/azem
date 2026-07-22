package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Viking602/azem/internal/i18n"
	"github.com/Viking602/azem/internal/session"
)

type fileChangeSection struct {
	Path             string `json:"path"`
	FirstChangedLine int    `json:"firstChangedLine"`
	Diff             string `json:"diff"`
}

func summarizeFileChange(name, arguments, structured, output string) (string, string, bool) {
	type editResult struct {
		Sections []fileChangeSection `json:"sections"`
	}

	var sections []fileChangeSection
	switch name {
	case "coding.edit_hashline":
		var result editResult
		if json.Unmarshal([]byte(structured), &result) == nil {
			sections = result.Sections
		}
		if len(sections) == 0 {
			sections = parseCompactEditOutput(output)
		}
	case "coding.write_file":
		var input struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if json.Unmarshal([]byte(arguments), &input) != nil || input.Path == "" {
			return "", "", false
		}
		lines := strings.Split(input.Content, "\n")
		if input.Content == "" {
			lines = nil
		} else if strings.HasSuffix(input.Content, "\n") {
			lines = lines[:len(lines)-1]
		}
		for index := range lines {
			lines[index] = "+" + lines[index]
		}
		sections = []fileChangeSection{{Path: input.Path, FirstChangedLine: 1, Diff: strings.Join(lines, "\n")}}
	default:
		return "", "", false
	}
	if len(sections) == 0 {
		return "", "", false
	}

	added, deleted := 0, 0
	chunks := make([]string, 0, len(sections))
	for _, section := range sections {
		if section.Path == "" {
			continue
		}
		for _, line := range strings.Split(section.Diff, "\n") {
			if strings.HasPrefix(line, "+") {
				added++
			} else if strings.HasPrefix(line, "-") {
				deleted++
			}
		}
		header := "@@ " + section.Path
		if section.FirstChangedLine > 0 {
			header += fmt.Sprintf(":%d", section.FirstChangedLine)
		}
		header += " @@"
		body := section.Diff
		if body == "" {
			body = "(empty file)"
		}
		chunks = append(chunks, header+"\n"+body)
	}
	if len(chunks) == 0 {
		return "", "", false
	}
	title := sections[0].Path
	if len(sections) > 1 {
		title = fmt.Sprintf("%d files", len(sections))
	}
	title += fmt.Sprintf("  +%d/-%d", added, deleted)
	return title, strings.Join(chunks, "\n\n"), true
}

func parseCompactEditOutput(output string) []fileChangeSection {
	var result []fileChangeSection
	var current *fileChangeSection
	var diffLines []string
	inDiff := false
	flush := func() {
		if current == nil {
			return
		}
		current.Diff = strings.Trim(strings.Join(diffLines, "\n"), "\n")
		if current.Path != "" && current.Diff != "" {
			result = append(result, *current)
		}
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "¶") {
			flush()
			path := strings.TrimPrefix(strings.SplitN(line, "#", 2)[0], "¶")
			current = &fileChangeSection{Path: path}
			diffLines = nil
			inDiff = false
			continue
		}
		if current == nil {
			continue
		}
		if value, ok := strings.CutPrefix(line, "firstChangedLine: "); ok {
			current.FirstChangedLine, _ = strconv.Atoi(strings.TrimSpace(value))
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
	return result
}

func summarizeToolArguments(name, arguments string, catalogs ...i18n.Catalog) string {
	catalog := i18n.Must(i18n.DefaultLanguage)
	if len(catalogs) > 0 {
		catalog = catalogs[0]
	}
	if strings.TrimSpace(arguments) == "" {
		return ""
	}
	var fields map[string]any
	if json.Unmarshal([]byte(arguments), &fields) != nil {
		return compactAgentActivity(arguments)
	}
	stringField := func(key string) string {
		value, _ := fields[key].(string)
		return strings.TrimSpace(value)
	}
	intField := func(key string) int {
		value, _ := fields[key].(float64)
		return int(value)
	}
	switch name {
	case "coding.edit_hashline":
		return summarizeEditArguments(stringField("input"), fields["dryRun"] == true, catalog)
	case "coding.write_file":
		if path := stringField("path"); path != "" {
			content, _ := fields["content"].(string)
			lines := 0
			if content != "" {
				lines = strings.Count(content, "\n") + 1
				if strings.HasSuffix(content, "\n") {
					lines--
				}
			}
			return catalog.T("tool.create_lines", map[string]string{"path": path, "count": strconv.Itoa(lines)})
		}
	case "coding.read_file":
		path := stringField("path")
		start, end := intField("startLine"), intField("endLine")
		if start == 0 && end > 0 {
			start = 1
		}
		if path != "" && start > 0 && end >= start {
			return catalog.T("tool.read_lines", map[string]string{"path": path, "start": strconv.Itoa(start), "end": strconv.Itoa(end)})
		}
		if path != "" {
			return catalog.T("tool.read", map[string]string{"path": path})
		}
	case "coding.go_test":
		if pkg := stringField("package"); pkg != "" {
			return catalog.T("tool.test_package", map[string]string{"package": pkg})
		}
	case "coding.shell":
		if command := stringField("command"); command != "" {
			return "$ " + compactAgentActivity(command)
		}
	case "coding.list_files":
		if pattern := first(stringField("pattern"), stringField("path")); pattern != "" {
			return catalog.T("tool.list", map[string]string{"path": pattern})
		}
	}
	if summary, ok := summarizeJSONOutput(arguments); ok {
		lines := strings.Split(summary, "\n")
		return strings.Join(lines[:min(4, len(lines))], "\n")
	}
	return compactAgentActivity(arguments)
}

func summarizeEditArguments(input string, dryRun bool, catalogs ...i18n.Catalog) string {
	catalog := i18n.Must(i18n.DefaultLanguage)
	if len(catalogs) > 0 {
		catalog = catalogs[0]
	}
	paths := make([]string, 0, 2)
	seen := make(map[string]bool)
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "¶") {
			continue
		}
		path := strings.TrimPrefix(strings.SplitN(line, "#", 2)[0], "¶")
		if path != "" && !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	action := catalog.T("tool.edit_action")
	if dryRun {
		action = catalog.T("tool.preview_action")
	}
	switch len(paths) {
	case 0:
		return catalog.T("tool.file_changes", map[string]string{"action": action})
	case 1:
		return catalog.T("tool.file_action", map[string]string{"action": action, "path": paths[0]})
	default:
		return catalog.T("tool.files_action", map[string]string{"action": action, "count": strconv.Itoa(len(paths)), "paths": strings.Join(paths[:min(3, len(paths))], ", ")})
	}
}

func summarizeToolFailure(name, output string, catalogs ...i18n.Catalog) string {
	catalog := i18n.Must(i18n.DefaultLanguage)
	if len(catalogs) > 0 {
		catalog = catalogs[0]
	}
	output = strings.TrimSpace(output)
	if output == "" {
		return catalog.T("tool.failed")
	}
	if summary, ok := summarizeJSONOutput(output); ok {
		output = summary
	}
	for _, prefix := range []string{name + " failed:", name + " rejected:"} {
		if detail, found := strings.CutPrefix(output, prefix); found {
			output = strings.TrimSpace(detail)
			break
		}
	}
	runes := []rune(output)
	if len(runes) > 600 {
		output = string(runes[:599]) + "…"
	}
	return output
}

func joinToolSummary(summary, detail string) string {
	if summary == "" {
		return detail
	}
	if detail == "" || detail == summary {
		return summary
	}
	return summary + "\n" + detail
}

func summarizeToolResult(name, arguments, output string, catalogs ...i18n.Catalog) string {
	catalog := i18n.Must(i18n.DefaultLanguage)
	if len(catalogs) > 0 {
		catalog = catalogs[0]
	}
	switch name {
	case "todo":
		var todo session.TodoList
		if json.Unmarshal([]byte(output), &todo) == nil {
			total, completed, current := 0, 0, ""
			for _, phase := range todo.Phases {
				for _, item := range phase.Items {
					total++
					if item.Status == session.TodoCompleted {
						completed++
					}
					if item.Status == session.TodoInProgress {
						current = item.Content
					}
				}
			}
			summary := catalog.T("tool.todo_updated", map[string]string{"completed": strconv.Itoa(completed), "total": strconv.Itoa(total)})
			if current != "" {
				summary += "\n" + catalog.T("tool.todo_current", map[string]string{"detail": current})
			}
			return summary
		}
		return catalog.T("tool.todo_updated_simple")
	case "subagent.get_output":
		catalog := i18n.Must(i18n.DefaultLanguage)
		if len(catalogs) > 0 {
			catalog = catalogs[0]
		}
		if summary, ok := summarizeSubagentOutput(output, catalog); ok {
			return summary
		}
		if summary, ok := summarizeJSONOutput(output); ok {
			return summary
		}
		return output
	case "coding.read_file":
		return summarizeReadFile(arguments, output, catalog)
	case "coding.list_files":
		return summarizeListOutput(output, 8)
	case "hydaelyn_activate_skill":
		catalog := i18n.Must(i18n.DefaultLanguage)
		if len(catalogs) > 0 {
			catalog = catalogs[0]
		}
		var input struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal([]byte(arguments), &input)
		if input.Name == "" {
			for _, line := range strings.Split(output, "\n") {
				if value, found := strings.CutPrefix(strings.TrimSpace(line), "--- skill: "); found {
					input.Name = strings.TrimSpace(strings.TrimSuffix(value, "---"))
					break
				}
			}
		}
		if input.Name == "" {
			input.Name = catalog.T("skill.unknown")
		}
		status := "skill.status.loaded"
		if strings.Contains(strings.ToLower(output), "already active") {
			status = "skill.status.active"
		}
		return catalog.T("skill.name", map[string]string{"name": input.Name}) + "\n" + catalog.T(status)
	default:
		if summary, ok := summarizeJSONOutput(output); ok {
			return summary
		}
		return output
	}
}

func summarizeSubagentOutput(output string, catalog i18n.Catalog) (string, bool) {
	var payload struct {
		Tasks []struct {
			TaskID      string `json:"task_id"`
			Status      string `json:"status"`
			Description string `json:"description"`
			Type        string `json:"type"`
			ElapsedMS   int64  `json:"elapsed_ms"`
			ToolCalls   int    `json:"tool_calls"`
			Turns       int    `json:"turns"`
			TokensUsed  int    `json:"tokens_used"`
			Output      string `json:"output"`
			Error       string `json:"error"`
			Warning     string `json:"warning"`
		} `json:"tasks"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(output)), &payload) != nil || payload.Tasks == nil {
		return "", false
	}
	if len(payload.Tasks) == 0 {
		return catalog.T("subagent.empty"), true
	}
	lines := make([]string, 0, len(payload.Tasks)*3)
	for index, task := range payload.Tasks {
		lines = append(lines, fmt.Sprintf("[%d] %s", index+1, first(task.Description, task.TaskID, catalog.T("block.subagent"))))
		status := localizedSubagentState(catalog, task.Status)
		stats := []string{status}
		if task.Type != "" {
			stats = append(stats, task.Type)
		}
		if task.Status != "not_found" {
			stats = append(stats,
				catalog.T("subagent.tools", map[string]string{"count": strconv.Itoa(task.ToolCalls)}),
				catalog.T("subagent.turns", map[string]string{"count": strconv.Itoa(task.Turns)}),
				catalog.T("subagent.tokens", map[string]string{"count": formatTokens(task.TokensUsed)}),
				catalog.T("subagent.seconds", map[string]string{"count": fmt.Sprintf("%.1f", float64(task.ElapsedMS)/1000)}),
			)
		}
		lines = append(lines, "    "+strings.Join(stats, " · "))
		switch {
		case strings.TrimSpace(task.Error) != "":
			lines = append(lines, "    "+catalog.T("subagent.error", map[string]string{"detail": compactSubagentSummaryText(task.Error)}))
		case strings.TrimSpace(task.Warning) != "":
			lines = append(lines, "    "+catalog.T("subagent.warning", map[string]string{"detail": compactSubagentSummaryText(task.Warning)}))
		case strings.TrimSpace(task.Output) != "":
			lines = append(lines, "    "+catalog.T("subagent.output", map[string]string{"detail": compactSubagentSummaryText(task.Output)}))
		}
	}
	return strings.Join(lines, "\n"), true
}

func localizedSubagentState(catalog i18n.Catalog, state string) string {
	keys := map[string]string{
		"initializing": "status.initializing", "queued": "status.queued", "running": "status.running",
		"cancelling": "status.cancelling", "completed": "status.completed", "failed": "status.failed",
		"cancelled": "status.cancelled", "interrupted": "status.interrupted", "not_found": "status.not_found",
	}
	if key := keys[state]; key != "" {
		return catalog.T(key)
	}
	return strings.ReplaceAll(state, "_", " ")
}

func compactSubagentSummaryText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 500 {
		return string(runes[:499]) + "…"
	}
	return value
}

func summarizeListOutput(output string, limit int) string {
	lines := make([]string, 0)
	truncation := ""
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[truncated;") {
			truncation = line
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) <= limit && truncation == "" {
		return strings.Join(lines, "\n")
	}
	shown := min(limit, len(lines))
	result := append([]string(nil), lines[:shown]...)
	if remaining := len(lines) - shown; remaining > 0 {
		result = append(result, fmt.Sprintf("… %d more entries (%d total)", remaining, len(lines)))
	}
	if truncation != "" {
		result = append(result, truncation)
	}
	return strings.Join(result, "\n")
}

func summarizeJSONOutput(output string) (string, bool) {
	var value any
	if json.Unmarshal([]byte(strings.TrimSpace(output)), &value) != nil {
		return "", false
	}
	lines := make([]string, 0, 8)
	appendJSONSummary(&lines, "", value, 8)
	if len(lines) == 0 {
		return "", false
	}
	return strings.Join(lines, "\n"), true
}

func appendJSONSummary(lines *[]string, key string, value any, limit int) {
	if len(*lines) >= limit {
		return
	}
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for childKey := range typed {
			keys = append(keys, childKey)
		}
		sort.Strings(keys)
		for _, childKey := range keys {
			childValue := typed[childKey]
			if text, ok := childValue.(string); ok && strings.TrimSpace(text) == "" {
				continue
			}
			label := childKey
			if key != "" {
				label = key + "." + childKey
			}
			appendJSONSummary(lines, label, childValue, limit)
			if len(*lines) >= limit {
				break
			}
		}
	case []any:
		label := key
		if label == "" {
			label = "items"
		}
		*lines = append(*lines, fmt.Sprintf("%s: %d items", label, len(typed)))
		shown := min(5, len(typed))
		for index := 0; index < shown && len(*lines) < limit; index++ {
			appendJSONSummary(lines, fmt.Sprintf("  [%d]", index+1), typed[index], limit)
		}
		if len(typed) > shown && len(*lines) < limit {
			*lines = append(*lines, fmt.Sprintf("  … %d more items", len(typed)-shown))
		}
	case nil:
		if key != "" {
			*lines = append(*lines, key+": null")
		}
	case string:
		text := strings.Join(strings.Fields(typed), " ")
		if key == "" {
			*lines = append(*lines, text)
		} else {
			*lines = append(*lines, key+": "+text)
		}
	default:
		text := fmt.Sprint(typed)
		if key == "" {
			*lines = append(*lines, text)
		} else {
			*lines = append(*lines, key+": "+text)
		}
	}
}

func summarizeReadFile(arguments, output string, catalogs ...i18n.Catalog) string {
	catalog := i18n.Must(i18n.DefaultLanguage)
	if len(catalogs) > 0 {
		catalog = catalogs[0]
	}
	var input struct {
		Path      string `json:"path"`
		StartLine int    `json:"startLine"`
		EndLine   int    `json:"endLine"`
	}
	_ = json.Unmarshal([]byte(arguments), &input)
	// Preserve governed source output for syntax highlighting, but retain the
	// compact summary for plain-text/error results that have no line protocol.
	if strings.Contains(output, "\n") && (strings.Contains(output, "\n¶") || strings.HasPrefix(output, "¶")) {
		return output
	}
	if input.Path == "" {
		return output
	}
	if input.StartLine > 0 && input.EndLine >= input.StartLine {
		return catalog.T("tool.read_lines", map[string]string{"path": input.Path, "start": strconv.Itoa(input.StartLine), "end": strconv.Itoa(input.EndLine)})
	}
	return catalog.T("tool.read", map[string]string{"path": input.Path})
}
