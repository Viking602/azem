package codex

import (
	"strings"
	"unicode"
)

var (
	pushWords = []string{
		"push", "git push", "推送", "推到", "推上去", "提交并推送", "push origin",
		"push it", "push this", "发到远程", "推到远程",
	}
	commitWords = []string{
		"commit", "git commit", "提交", "提交代码", "提交更改", "提交修改",
	}
	deleteWords = []string{
		"delete", "remove", "rm -rf", "删掉", "删除", "清掉", "去掉",
	}
	networkWords = []string{
		"download", "fetch", "curl", "wget", "安装依赖", "下载", "拉取",
		"npm install", "bun install", "go get",
	}
)

// ScoreUserAuthorization implements Codex's transcript authorization scoring
// against the current turn goal and the planned action. Without this, high-risk
// actions the user explicitly asked for (git push after "帮我提交并推送") stay
// unknown and the host matrix incorrectly falls back to a person.
func ScoreUserAuthorization(goal, toolName, target, command string) string {
	goal = compactAuthText(goal)
	if goal == "" {
		return "unknown"
	}
	action := compactAuthText(toolName + " " + target + " " + command)
	switch {
	case actionMentions(action, "git push", "push origin") || (strings.Contains(action, "git") && strings.Contains(action, "push")):
		if containsAnyFold(goal, pushWords...) {
			return "high"
		}
		return "unknown"
	case actionMentions(action, "git commit") || (strings.Contains(action, "git") && strings.Contains(action, "commit")):
		if containsAnyFold(goal, commitWords...) || containsAnyFold(goal, pushWords...) {
			return "high"
		}
		return "unknown"
	case actionMentions(action, "rm -rf", "rm -fr") || strings.Contains(action, "rm -"):
		if !containsAnyFold(goal, deleteWords...) {
			return "unknown"
		}
		if authMentionsTarget(goal, target, command) {
			return "high"
		}
		return "medium"
	case actionMentions(action, "curl", "wget", "network"):
		if containsAnyFold(goal, networkWords...) || containsAnyFold(goal, pushWords...) {
			return "high"
		}
		return "unknown"
	default:
		return "unknown"
	}
}

func compactAuthText(value string) string {
	var b strings.Builder
	prevSpace := true
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
			}
			prevSpace = true
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

func containsAnyFold(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(haystack, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func actionMentions(action string, needles ...string) bool {
	return containsAnyFold(action, needles...)
}

func authMentionsTarget(goal, target, command string) bool {
	for _, candidate := range []string{target, command} {
		base := candidate
		if i := strings.LastIndexAny(base, `/\`); i >= 0 && i+1 < len(base) {
			base = base[i+1:]
		}
		base = strings.TrimSpace(base)
		if len(base) >= 3 && !strings.EqualFold(base, "workspace") && strings.Contains(goal, strings.ToLower(base)) {
			return true
		}
	}
	return false
}
