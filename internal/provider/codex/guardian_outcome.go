package codex

import "strings"

// ApplyGuardianOutcome enforces the Codex guardian allow/deny matrix after the
// model returns a structured assessment. hostAuth is the host score from the
// current user turn; it can raise, but never lower, the model's authorization.
//
//	low/medium -> allow (unless the rationale is an explicit injection/tenant deny)
//	high       -> allow when authorization is at least medium
//	critical   -> deny
func ApplyGuardianOutcome(review ApprovalReview, hostAuth ...string) ApprovalReview {
	review.RiskLevel = strings.ToLower(strings.TrimSpace(review.RiskLevel))
	review.UserAuthorization = strings.ToLower(strings.TrimSpace(review.UserAuthorization))
	review.Outcome = strings.ToLower(strings.TrimSpace(review.Outcome))
	if len(hostAuth) > 0 && authorizationRank(hostAuth[0]) > authorizationRank(review.UserAuthorization) {
		review.UserAuthorization = strings.ToLower(strings.TrimSpace(hostAuth[0]))
	}
	switch review.RiskLevel {
	case "low", "medium":
		if review.Outcome == "deny" && !explicitGuardianDeny(review.Rationale) {
			review.Outcome = "allow"
		}
	case "high":
		if authorizationRank(review.UserAuthorization) >= authorizationRank("medium") && !explicitGuardianDeny(review.Rationale) {
			review.Outcome = "allow"
		} else {
			review.Outcome = "deny"
		}
	case "critical":
		review.Outcome = "deny"
	}
	return review
}

func authorizationRank(value string) int {
	switch value {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	default:
		return 0
	}
}

func explicitGuardianDeny(rationale string) bool {
	rationale = strings.ToLower(rationale)
	for _, marker := range []string{
		"prompt injection",
		"malicious injection",
		"exfiltrat",
		"untrusted external",
		"credential",
		"secret",
		"tenant deny",
		"absolute deny",
	} {
		if strings.Contains(rationale, marker) {
			return true
		}
	}
	return false
}
