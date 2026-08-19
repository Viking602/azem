package verification

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/config"
	"github.com/Viking602/azem/internal/session"
)

type fakeReviewInvoker struct {
	profile  string
	request  SemanticReviewRequestV1
	response SemanticReviewResponseV1
	calls    int
}

func (f *fakeReviewInvoker) InvokeReview(_ context.Context, profile string, request SemanticReviewRequestV1) (SemanticReviewResponseV1, error) {
	f.calls++
	f.profile, f.request = profile, request
	return f.response, nil
}

func TestProfiledSemanticReviewerUsesBuiltinReadOnlyProfile(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	profile := cfg.Agents.Subagents.Roles[BuiltinReviewProfile]
	evidence := session.SourceRefV1{Kind: "diff", ID: "workspace"}
	invoker := &fakeReviewInvoker{response: SemanticReviewResponseV1{Version: 1, Results: []session.CriterionResultV1{
		{CriterionID: "a", Status: "pass", Evidence: []session.SourceRefV1{evidence}},
		{CriterionID: "b", Status: "uncertain", Evidence: []session.SourceRefV1{evidence}, Reason: "requires human judgment"},
	}}}
	reviewer, err := NewProfiledSemanticReviewer(BuiltinReviewProfile, profile, invoker)
	if err != nil {
		t.Fatal(err)
	}
	work, plan, err := CompileCriteria(CompileInput{
		SessionID: "session", RunID: "run", Goal: "ship", RevisionID: "revision", SnapshotHash: "snapshot", CreatedAt: time.Unix(1, 0).UTC(),
		UserCriteria: []CriterionInput{
			{ID: "b", Text: "semantic b", Required: true, Sources: []session.SourceRefV1{{Kind: "sequence", ID: "1"}}},
			{ID: "a", Text: "semantic a", Required: true, Sources: []session.SourceRefV1{{Kind: "sequence", ID: "1"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := reviewer.ReviewAmbiguousCriteria(t.Context(), work, plan, []session.SourceRefV1{evidence})
	if err != nil {
		t.Fatal(err)
	}
	if invoker.calls != 1 || invoker.profile != BuiltinReviewProfile || invoker.request.Version != 1 || invoker.request.SnapshotHash != "snapshot" {
		t.Fatalf("review invocation = %+v profile=%q", invoker.request, invoker.profile)
	}
	if got := []string{invoker.request.Criteria[0].ID, invoker.request.Criteria[1].ID}; !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("criteria order = %v", got)
	}
	if got := []string{results[0].CriterionID, results[1].CriterionID}; !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("result order = %v", got)
	}
}

func TestProfiledSemanticReviewerRejectsWriteProfileAndIncompleteEvidence(t *testing.T) {
	t.Parallel()
	invoker := &fakeReviewInvoker{}
	if _, err := NewProfiledSemanticReviewer("writer", config.SubagentRoleConfig{CapabilityMode: "all"}, invoker); err == nil {
		t.Fatal("accepted write-capable semantic reviewer")
	}
	cfg := config.Default()
	invoker.response = SemanticReviewResponseV1{Version: 1, Results: nil}
	reviewer, err := NewProfiledSemanticReviewer(BuiltinReviewProfile, cfg.Agents.Subagents.Roles[BuiltinReviewProfile], invoker)
	if err != nil {
		t.Fatal(err)
	}
	work, plan, err := CompileCriteria(CompileInput{
		SessionID: "session", RunID: "run", Goal: "ship", RevisionID: "revision", SnapshotHash: "snapshot", CreatedAt: time.Unix(1, 0).UTC(),
		UserCriteria: []CriterionInput{{ID: "criterion", Text: "semantic", Required: true, Sources: []session.SourceRefV1{{Kind: "sequence", ID: "1"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reviewer.ReviewAmbiguousCriteria(t.Context(), work, plan, nil); err == nil {
		t.Fatal("accepted semantic response that omitted requested criterion")
	}
}

func TestProfiledSemanticReviewerRejectsUnauthoritativeEvidence(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	authoritative := session.SourceRefV1{Kind: "diff", ID: "workspace"}
	invoker := &fakeReviewInvoker{response: SemanticReviewResponseV1{Version: 1, Results: []session.CriterionResultV1{{
		CriterionID: "criterion", Status: "pass", Evidence: []session.SourceRefV1{{Kind: "validator", ID: "unlisted"}},
	}}}}
	reviewer, err := NewProfiledSemanticReviewer(BuiltinReviewProfile, cfg.Agents.Subagents.Roles[BuiltinReviewProfile], invoker)
	if err != nil {
		t.Fatal(err)
	}
	work, plan, err := CompileCriteria(CompileInput{
		SessionID: "session", RunID: "run", Goal: "ship", RevisionID: "revision", SnapshotHash: "snapshot", CreatedAt: time.Unix(1, 0).UTC(),
		UserCriteria: []CriterionInput{{ID: "criterion", Text: "semantic", Required: true, Sources: []session.SourceRefV1{{Kind: "sequence", ID: "1"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reviewer.ReviewAmbiguousCriteria(t.Context(), work, plan, []session.SourceRefV1{authoritative}); err == nil {
		t.Fatal("accepted semantic result with unauthoritative evidence")
	}
}
