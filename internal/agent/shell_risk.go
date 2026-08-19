package agent

import (
	"encoding/json"
	"regexp"
	"strings"
)

var (
	highRiskShellPattern = regexp.MustCompile(`(?i)(?:` +
		`\brm\s+-[^\n]*[rf][^\n]*[rf]` +
		`|\bgit\s+push\b` +
		`|\bgit\s+reset\s+--hard\b` +
		`|\bgit\s+clean\b` +
		`|\bsudo\b|\bdoas\b` +
		`|\bchmod\s+(-R\s+)?0?777\b` +
		`|\b(curl|wget|nc|ncat|ssh|scp|sftp|rsync)\b` +
		`|\|\s*(sh|bash|zsh|fish|pwsh|powershell)\b` +
		`|\beval\b` +
		`|\bdd\s+if=` +
		`|\bmkfs\b|\bdiskutil\b` +
		`|\bkill\s+-9\b|\bpkill\b|\bkillall\b` +
		`|\bdocker\s+(rm|rmi|system\s+prune)\b` +
		`|\bkubectl\s+delete\b` +
		`|\b(npm|pnpm)\s+publish\b` +
		`|\bgh\s+release\b` +
		`|\bosascript\b|\blaunchctl\b` +
		`)`)
	sensitiveShellPathPattern = regexp.MustCompile(`(?i)(?:^|[\s"'=])(?:~|/[^/\s]+(?:/[^/\s]+)*)?/(\.ssh|\.aws|\.gnupg|\.config/gh)(?:/|[\s"'$])|(?:^|[/\s"'=])(\.env(?:\.[^/\s"']+)?|credentials\.(json|ya?ml)|azem\.db)(?:$|[\s"'$])`)
	lowRiskShellPattern       = regexp.MustCompile(`(?i)^\s*(` +
		`go\s+(test|fmt|vet|env|list|mod\s+download)` +
		`|gofmt` +
		`|git\s+(status|diff|log|show|branch|rev-parse|describe)` +
		`|(ls|pwd|cat|head|tail|wc|file|stat|date|whoami|uname|hostname)` +
		`|(rg|grep|egrep|fgrep)` +
		`|(bun|npm|yarn|pnpm)\s+test` +
		`)(\s|$)`)
)

// ClassifyShellRisk maps a planned coding.shell command onto the Codex
// guardian taxonomy. Unknown local commands stay medium so they can auto-allow;
// only destructive, network, or privilege-changing commands stay high.
func ClassifyShellRisk(command string, network bool) string {
	command = strings.TrimSpace(command)
	if network || command == "" || highRiskShellPattern.MatchString(command) || sensitiveShellPathPattern.MatchString(command) {
		return "high"
	}
	if hasShellComposition(command) {
		return "high"
	}
	if findDeletes(command) {
		return "high"
	}
	if lowRiskShellPattern.MatchString(command) {
		return "low"
	}
	return "medium"
}

func findDeletes(command string) bool {
	lower := strings.ToLower(command)
	return strings.Contains(lower, "find ") && strings.Contains(lower, "-delete")
}

func hasShellComposition(command string) bool {
	var quote byte
	escaped := false
	for index := 0; index < len(command); index++ {
		current := command[index]
		if escaped {
			escaped = false
			continue
		}
		if quote == '\'' {
			if current == '\'' {
				quote = 0
			}
			continue
		}
		if current == '\\' {
			escaped = true
			continue
		}
		if quote == '"' {
			if current == '"' {
				quote = 0
				continue
			}
			if current == '`' || current == '$' && index+1 < len(command) && command[index+1] == '(' {
				return true
			}
			continue
		}
		switch current {
		case '\'', '"':
			quote = current
		case '|', '&', ';', '<', '>', '`', '\n', '\r':
			return true
		case '$':
			if index+1 < len(command) && command[index+1] == '(' {
				return true
			}
		}
	}
	return false
}

func shellCallCommand(arguments json.RawMessage) string {
	var input struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(arguments, &input) != nil {
		return ""
	}
	return input.Command
}
