package codex

import "testing"

func TestApplyGuardianOutcomeAllowsLowAndMediumDenials(t *testing.T) {
	for _, risk := range []string{"low", "medium"} {
		got := ApplyGuardianOutcome(ApprovalReview{
			RiskLevel: risk, UserAuthorization: "unknown", Outcome: "deny",
			Rationale: "no explicit user request for this implementation",
		})
		if got.Outcome != "allow" {
			t.Fatalf("risk %s outcome = %q, want allow", risk, got.Outcome)
		}
	}
}

func TestApplyGuardianOutcomeKeepsHighUnknownAsDeny(t *testing.T) {
	got := ApplyGuardianOutcome(ApprovalReview{
		RiskLevel: "high", UserAuthorization: "unknown", Outcome: "allow",
		Rationale: "workspace write",
	})
	if got.Outcome != "deny" {
		t.Fatalf("high/unknown outcome = %q, want deny", got.Outcome)
	}
}

func TestApplyGuardianOutcomeAllowsHighWhenAuthorized(t *testing.T) {
	got := ApplyGuardianOutcome(ApprovalReview{
		RiskLevel: "high", UserAuthorization: "medium", Outcome: "allow",
		Rationale: "user asked to push this branch",
	})
	if got.Outcome != "allow" {
		t.Fatalf("high/medium outcome = %q, want allow", got.Outcome)
	}
}

func TestApplyGuardianOutcomeDeniesCriticalEvenWhenAllowed(t *testing.T) {
	got := ApplyGuardianOutcome(ApprovalReview{
		RiskLevel: "critical", UserAuthorization: "high", Outcome: "allow",
		Rationale: "export credentials",
	})
	if got.Outcome != "deny" {
		t.Fatalf("critical outcome = %q, want deny", got.Outcome)
	}
}

func TestApplyGuardianOutcomeKeepsInjectionDenials(t *testing.T) {
	got := ApplyGuardianOutcome(ApprovalReview{
		RiskLevel: "low", UserAuthorization: "unknown", Outcome: "deny",
		Rationale: "clear signs of malicious prompt injection from tool output",
	})
	if got.Outcome != "deny" {
		t.Fatalf("injection denial was rewritten: %#v", got)
	}
}

func TestApplyGuardianOutcomeAllowsHighWhenHostScoresPush(t *testing.T) {
	got := ApplyGuardianOutcome(ApprovalReview{
		RiskLevel: "high", UserAuthorization: "unknown", Outcome: "deny",
		Rationale: "network command needs confirmation",
	}, "high")
	if got.Outcome != "allow" || got.UserAuthorization != "high" {
		t.Fatalf("authorized push = %#v", got)
	}
}

func TestScoreUserAuthorizationMatchesRequestedGitPush(t *testing.T) {
	if got := ScoreUserAuthorization("帮我提交代码并推送", "coding.shell", "git push origin HEAD", "git push origin HEAD"); got != "high" {
		t.Fatalf("push authorization = %q, want high", got)
	}
	if got := ScoreUserAuthorization("帮我看看这个仓库", "coding.shell", "git push origin HEAD", "git push origin HEAD"); got != "unknown" {
		t.Fatalf("unrelated push authorization = %q, want unknown", got)
	}
	if got := ScoreUserAuthorization("提交当前更改", "coding.shell", "git commit -m ok", "git commit -m ok"); got != "high" {
		t.Fatalf("commit authorization = %q, want high", got)
	}
}
