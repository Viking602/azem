package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/contextarchive"
	"github.com/Viking602/azem/internal/session"
	"github.com/Viking602/venat/message"
)

const (
	contextRebuildPolicyVersion       = 3
	contextArchiveManifestMetadataKey = "azem.context.archive_manifest_v1"
)

type ContextSegmentV1 struct {
	Kind          string   `json:"kind"`
	Mandatory     bool     `json:"mandatory"`
	TokenEstimate int      `json:"token_estimate"`
	ContentHash   string   `json:"content_hash"`
	SourceRefs    []string `json:"source_refs,omitempty"`
}

type ContextExclusionV1 struct {
	SourceRef string `json:"source_ref"`
	Reason    string `json:"reason"`
}

type ArchiveContextManifestV1 struct {
	Version            int                      `json:"version"`
	ID                 string                   `json:"id"`
	SessionID          string                   `json:"session_id"`
	RunID              string                   `json:"run_id,omitempty"`
	Reason             string                   `json:"reason"`
	PolicyVersion      int                      `json:"policy_version"`
	StaticIdentity     string                   `json:"static_identity"`
	ModelRouteHash     string                   `json:"model_route_hash"`
	CanonicalHighWater int64                    `json:"canonical_high_water"`
	TodoRevision       int64                    `json:"todo_revision"`
	TargetTokens       int                      `json:"target_tokens"`
	EstimatedTokens    int                      `json:"estimated_tokens"`
	Segments           []ContextSegmentV1       `json:"segments"`
	Exclusions         []ContextExclusionV1     `json:"exclusions,omitempty"`
	Archive            *contextarchive.Manifest `json:"archive"`
	ManifestHash       string                   `json:"manifest_hash"`
}

func newArchiveContextManifest(c turnContext, reason string, source, result, omitted []message.Message, archive contextarchive.Manifest, target int) ArchiveContextManifestV1 {
	canonicalHighWater := canonicalMessageHighWater(source)
	if c.canonicalHighWater != nil {
		canonicalHighWater = *c.canonicalHighWater
	}
	manifest := ArchiveContextManifestV1{
		Version:            1,
		SessionID:          c.sessionID,
		RunID:              c.runID,
		Reason:             reason,
		PolicyVersion:      contextRebuildPolicyVersion,
		StaticIdentity:     c.staticIdentity,
		ModelRouteHash:     hashText(c.providerID + "\x00" + c.modelID),
		CanonicalHighWater: canonicalHighWater,
		TodoRevision:       c.todo.Revision,
		TargetTokens:       target,
		EstimatedTokens:    estimateContextTokens(result),
		Archive:            &archive,
	}
	manifest.Segments = archiveContextManifestSegments(result, c.runID)
	manifest.Exclusions = archiveContextExclusions(omitted, c.runID)
	manifest.ManifestHash = archiveContextManifestHash(manifest)
	manifest.ID = "context-" + manifest.ManifestHash[:24]
	return manifest
}

func canonicalMessageHighWater(messages []message.Message) int64 {
	highWater := int64(-1)
	for _, current := range messages {
		sequence, err := strconv.ParseInt(current.Metadata[sourceSequenceMetadataKey], 10, 64)
		if err == nil && sequence > highWater {
			highWater = sequence
		}
	}
	return highWater
}

func archiveContextManifestSegments(messages []message.Message, runID string) []ContextSegmentV1 {
	segments := make([]ContextSegmentV1, 0, len(messages))
	for _, current := range messages {
		refs := messageStableReferences(current, runID)
		if archive, ok := archiveManifestFromMessage(current); ok && archive.SourceArtifactID != "" {
			refs = boundedUniqueStrings(append(refs, "artifact:"+archive.SourceArtifactID), 32, 1024)
		}
		segments = append(segments, ContextSegmentV1{
			Kind:          archiveManifestSegmentKind(current),
			Mandatory:     true,
			TokenEstimate: estimateContextTokens([]message.Message{current}),
			ContentHash:   messageContentHash(current),
			SourceRefs:    refs,
		})
	}
	return segments
}

func archiveManifestSegmentKind(current message.Message) string {
	_, archived := archiveManifestFromMessage(current)
	switch {
	case archived:
		return "archive_carrier"
	case strings.HasPrefix(current.Text, todoReminderPrefix):
		return "todo"
	case current.Role == message.RoleSystem:
		return "core"
	case current.Role == message.RoleUser:
		return "recent_user"
	default:
		return "hot_tail"
	}
}

func archiveContextExclusions(messages []message.Message, runID string) []ContextExclusionV1 {
	refs := make([]string, 0, len(messages))
	for _, current := range messages {
		refs = append(refs, messageStableReferences(current, runID)...)
	}
	refs = boundedUniqueStrings(refs, 4096, 4096)
	sort.Strings(refs)
	exclusions := make([]ContextExclusionV1, 0, len(refs))
	for _, ref := range refs {
		exclusions = append(exclusions, ContextExclusionV1{SourceRef: ref, Reason: "represented_by_archive"})
	}
	return exclusions
}

func archiveContextManifestHash(manifest ArchiveContextManifestV1) string {
	manifest.ID, manifest.ManifestHash = "", ""
	encoded, _ := json.Marshal(manifest)
	return hashText(string(encoded))
}

func attachArchiveContextManifest(carrier message.Message, manifest ArchiveContextManifestV1) (message.Message, error) {
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return carrier, err
	}
	if carrier.Metadata == nil {
		carrier.Metadata = make(map[string]string, 2)
	}
	carrier.Metadata[contextArchiveManifestMetadataKey] = string(manifestJSON)
	return carrier, nil
}

func extractArchiveContextManifest(messages []message.Message) *ArchiveContextManifestV1 {
	for index := len(messages) - 1; index >= 0; index-- {
		current := messages[index]
		if current.Kind != message.KindCompactionSummary {
			continue
		}
		var manifest ArchiveContextManifestV1
		if json.Unmarshal([]byte(current.Metadata[contextArchiveManifestMetadataKey]), &manifest) != nil ||
			manifest.Version != 1 || manifest.Archive == nil || manifest.ManifestHash == "" ||
			manifest.ManifestHash != archiveContextManifestHash(manifest) || len(manifest.ManifestHash) < 24 ||
			manifest.ID != "context-"+manifest.ManifestHash[:24] {
			continue
		}
		return &manifest
	}
	return nil
}

func extractArchiveContextManifestRecord(messages []message.Message) *session.ContextManifestRecord {
	manifest := extractArchiveContextManifest(messages)
	if manifest == nil {
		return nil
	}
	data, _ := json.Marshal(manifest)
	highWater := manifest.CanonicalHighWater
	return &session.ContextManifestRecord{
		ID:                 manifest.ID,
		RunID:              manifest.RunID,
		CanonicalHighWater: &highWater,
		PolicyVersion:      manifest.PolicyVersion,
		ManifestHash:       manifest.ManifestHash,
		Data:               data,
	}
}

func messageStableReferences(value message.Message, runID string) []string {
	refs := make([]string, 0, 2)
	if sequence := value.Metadata[sourceSequenceMetadataKey]; sequence != "" {
		refs = append(refs, "sequence:"+sequence)
	}
	if result := value.ToolResult; result != nil {
		var artifact struct {
			Artifact string `json:"artifact_ref"`
		}
		if json.Unmarshal([]byte(result.Content), &artifact) == nil && artifact.Artifact != "" {
			refs = append(refs, "artifact:"+artifact.Artifact)
		} else if runID != "" && result.ToolCallID != "" {
			refs = append(refs, "tool:"+runID+":"+result.ToolCallID)
		}
	}
	return refs
}

func messageContentHash(value message.Message) string {
	setMessageCreatedAt(&value, time.Time{})
	encoded, _ := json.Marshal(value)
	return hashText(string(encoded))
}

func hashText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func boundedUniqueStrings(values []string, maximum, maxBytes int) []string {
	result := make([]string, 0, min(len(values), maximum))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ToValidUTF8(value, "�"))
		if value == "" || len(value) > maxBytes {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
		if len(result) == maximum {
			break
		}
	}
	return result
}
