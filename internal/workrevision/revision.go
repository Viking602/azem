package workrevision

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/session"
)

const MaxRevisionFiles = 4096

type DeriveInput struct {
	SessionID             string
	CanonicalUserSequence int64
	TodoRevision          int64
	SemanticRevision      int64
	ApprovedPlanID        string
	ExplicitConstraints   []session.CriterionV1
	GitHEAD               string
	Files                 []session.WorkRevisionFileV1
	CreatedAt             time.Time
}

func Derive(input DeriveInput) (session.WorkRevisionV1, error) {
	if strings.TrimSpace(input.SessionID) == "" || input.CanonicalUserSequence < 0 || input.TodoRevision < 0 || input.SemanticRevision < 0 || input.CreatedAt.IsZero() {
		return session.WorkRevisionV1{}, fmt.Errorf("work revision: invalid durable cursor")
	}
	files, err := canonicalFiles(input.Files)
	if err != nil {
		return session.WorkRevisionV1{}, err
	}
	constraints, err := canonicalConstraints(input.ExplicitConstraints)
	if err != nil {
		return session.WorkRevisionV1{}, err
	}
	constraintDigest := digestJSON(constraints)
	snapshot := struct {
		GitHEAD string                       `json:"git_head"`
		Files   []session.WorkRevisionFileV1 `json:"files"`
	}{GitHEAD: strings.TrimSpace(input.GitHEAD), Files: files}
	snapshotHash := digestJSON(snapshot)
	identity := struct {
		SessionID             string `json:"session_id"`
		CanonicalUserSequence int64  `json:"canonical_user_sequence"`
		TodoRevision          int64  `json:"todo_revision"`
		SemanticRevision      int64  `json:"semantic_revision"`
		ApprovedPlanID        string `json:"approved_plan_id"`
		ConstraintDigest      string `json:"constraint_digest"`
		SnapshotHash          string `json:"snapshot_hash"`
	}{
		SessionID: input.SessionID, CanonicalUserSequence: input.CanonicalUserSequence, TodoRevision: input.TodoRevision,
		SemanticRevision: input.SemanticRevision, ApprovedPlanID: strings.TrimSpace(input.ApprovedPlanID),
		ConstraintDigest: constraintDigest, SnapshotHash: snapshotHash,
	}
	revision := session.WorkRevisionV1{
		Version: session.WorkContractVersionV1, ID: "work-revision:" + digestJSON(identity)[:24], SessionID: input.SessionID,
		CanonicalUserSequence: input.CanonicalUserSequence, TodoRevision: input.TodoRevision, SemanticRevision: input.SemanticRevision,
		ApprovedPlanID: strings.TrimSpace(input.ApprovedPlanID), ConstraintDigest: constraintDigest, Constraints: constraints,
		GitHEAD: snapshot.GitHEAD, Files: files, SnapshotHash: snapshotHash, CreatedAt: input.CreatedAt.UTC(),
	}
	return revision, revision.Validate()
}

func canonicalConstraints(criteria []session.CriterionV1) ([]session.CriterionV1, error) {
	result := make([]session.CriterionV1, 0, len(criteria))
	seen := make(map[string]struct{}, len(criteria))
	for _, criterion := range criteria {
		if criterion.Origin != "explicit" {
			continue
		}
		if strings.TrimSpace(criterion.ID) == "" || strings.TrimSpace(criterion.Text) == "" {
			return nil, fmt.Errorf("work revision: explicit constraint requires id and text")
		}
		if _, exists := seen[criterion.ID]; exists {
			return nil, fmt.Errorf("work revision: duplicate explicit constraint %q", criterion.ID)
		}
		seen[criterion.ID] = struct{}{}
		for _, source := range criterion.Sources {
			if err := source.Validate(); err != nil {
				return nil, err
			}
		}
		criterion.Sources = append([]session.SourceRefV1(nil), criterion.Sources...)
		sort.Slice(criterion.Sources, func(i, j int) bool {
			left, right := criterion.Sources[i], criterion.Sources[j]
			return left.Kind+"\x00"+left.ID+"\x00"+left.Range+"\x00"+left.SHA256 < right.Kind+"\x00"+right.ID+"\x00"+right.Range+"\x00"+right.SHA256
		})
		result = append(result, criterion)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}
func canonicalFiles(files []session.WorkRevisionFileV1) ([]session.WorkRevisionFileV1, error) {
	if len(files) > MaxRevisionFiles {
		return nil, fmt.Errorf("work revision: %d files exceeds limit %d", len(files), MaxRevisionFiles)
	}
	byPath := make(map[string]session.WorkRevisionFileV1, len(files))
	for _, file := range files {
		path := filepath.ToSlash(filepath.Clean(strings.TrimSpace(file.Path)))
		if path == "" || path == "." || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, "../") || (!file.Observed && !file.Touched) {
			return nil, fmt.Errorf("work revision: unsafe or unowned file %q", file.Path)
		}
		digest, err := hex.DecodeString(file.SHA256)
		if err != nil || len(digest) != sha256.Size {
			return nil, fmt.Errorf("work revision: invalid sha256 for %q", path)
		}
		file.Path = path
		file.SHA256 = strings.ToLower(file.SHA256)
		if previous, exists := byPath[path]; exists {
			if previous.SHA256 != file.SHA256 {
				return nil, fmt.Errorf("work revision: conflicting hashes for %q", path)
			}
			file.Observed = file.Observed || previous.Observed
			file.Touched = file.Touched || previous.Touched
		}
		byPath[path] = file
	}
	result := make([]session.WorkRevisionFileV1, 0, len(byPath))
	for _, file := range byPath {
		result = append(result, file)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func digestJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
