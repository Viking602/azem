package workrevision

import (
	"strings"
	"testing"
	"time"

	"github.com/Viking602/azem/internal/session"
)

func TestDeriveIsStableAcrossInputOrderAndTracksBoundedFacts(t *testing.T) {
	t.Parallel()
	created := time.Unix(100, 0).UTC()
	digestA := strings.Repeat("a", 64)
	digestB := strings.Repeat("b", 64)
	input := DeriveInput{
		SessionID: "session", CanonicalUserSequence: 42, TodoRevision: 3, SemanticRevision: 7, ApprovedPlanID: "plan",
		ExplicitConstraints: []session.CriterionV1{
			{ID: "b", Text: "second", Origin: "explicit", Sources: []session.SourceRefV1{{Kind: "sequence", ID: "2"}}},
			{ID: "ignored", Text: "inferred", Origin: "inferred"},
			{ID: "a", Text: "first", Origin: "explicit", Sources: []session.SourceRefV1{{Kind: "sequence", ID: "1"}}},
		},
		GitHEAD: "abc", Files: []session.WorkRevisionFileV1{
			{Path: "internal/b.go", SHA256: digestB, Touched: true},
			{Path: "internal/a.go", SHA256: digestA, Observed: true},
		}, CreatedAt: created,
	}
	first, err := Derive(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Files[0], input.Files[1] = input.Files[1], input.Files[0]
	input.ExplicitConstraints[0], input.ExplicitConstraints[2] = input.ExplicitConstraints[2], input.ExplicitConstraints[0]
	second, err := Derive(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.SnapshotHash != second.SnapshotHash || first.ConstraintDigest != second.ConstraintDigest {
		t.Fatalf("order changed revision: first=%+v second=%+v", first, second)
	}
	if len(first.Files) != 2 || first.Files[0].Path != "internal/a.go" || first.Files[1].Path != "internal/b.go" {
		t.Fatalf("canonical files = %+v", first.Files)
	}
}

func TestDeriveChangesOnlyAffectedRevisionComponents(t *testing.T) {
	t.Parallel()
	input := DeriveInput{
		SessionID: "session", CanonicalUserSequence: 1, ExplicitConstraints: []session.CriterionV1{{ID: "constraint", Text: "keep API", Origin: "explicit"}},
		GitHEAD: "head", Files: []session.WorkRevisionFileV1{{Path: "a.go", SHA256: strings.Repeat("a", 64), Observed: true}}, CreatedAt: time.Unix(1, 0).UTC(),
	}
	base, err := Derive(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Files[0].SHA256 = strings.Repeat("b", 64)
	changedFile, err := Derive(input)
	if err != nil {
		t.Fatal(err)
	}
	if base.SnapshotHash == changedFile.SnapshotHash || base.ConstraintDigest != changedFile.ConstraintDigest || base.ID == changedFile.ID {
		t.Fatalf("file change was not isolated: base=%+v changed=%+v", base, changedFile)
	}
	input.Files[0].SHA256 = strings.Repeat("a", 64)
	input.ExplicitConstraints[0].Text = "change API"
	changedConstraint, err := Derive(input)
	if err != nil {
		t.Fatal(err)
	}
	if base.SnapshotHash != changedConstraint.SnapshotHash || base.ConstraintDigest == changedConstraint.ConstraintDigest || base.ID == changedConstraint.ID {
		t.Fatalf("constraint change was not isolated: base=%+v changed=%+v", base, changedConstraint)
	}
}

func TestDeriveRejectsUnboundedOrUnsafeFileInputs(t *testing.T) {
	t.Parallel()
	base := DeriveInput{SessionID: "session", CreatedAt: time.Unix(1, 0).UTC()}
	for _, file := range []session.WorkRevisionFileV1{
		{Path: "../secret", SHA256: strings.Repeat("a", 64), Observed: true},
		{Path: "safe.go", SHA256: strings.Repeat("a", 64)},
		{Path: "safe.go", SHA256: "bad", Touched: true},
	} {
		input := base
		input.Files = []session.WorkRevisionFileV1{file}
		if _, err := Derive(input); err == nil {
			t.Fatalf("accepted file %+v", file)
		}
	}
	input := base
	input.Files = make([]session.WorkRevisionFileV1, MaxRevisionFiles+1)
	if _, err := Derive(input); err == nil {
		t.Fatal("accepted unbounded file list")
	}
}
