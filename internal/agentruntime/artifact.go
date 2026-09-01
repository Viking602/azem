package agentruntime

import (
	"context"
	"io"
	"time"
)

// Artifact is a content-addressed produced output. The application owns
// the blob backend; the framework only names the metadata the rest of
// the runtime can point at via BlackboardItem.ArtifactRefs.
//
// Spec anchor: ADR-011 (four-layer context), ADR-027 (contract only).
type Artifact struct {
	ID         string            `json:"id"`
	URI        string            `json:"uri,omitempty"`
	MimeType   string            `json:"mimeType,omitempty"`
	Checksum   string            `json:"checksum,omitempty"`
	ProducedBy string            `json:"producedBy,omitempty"`
	RunID      string            `json:"runId,omitempty"`
	TaskID     string            `json:"taskId,omitempty"`
	Size       int64             `json:"size,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	CreatedAt  time.Time         `json:"createdAt,omitempty"`
}

// ArtifactSelector filters ArtifactStore.List. All set fields AND-combine.
// Limit 0 means unbounded.
type ArtifactSelector struct {
	RunID    string `json:"runId,omitempty"`
	TaskID   string `json:"taskId,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// ArtifactStore is the optional Position D contract for large or binary
// outputs. The framework ships no backend. Applications implement this
// against their own object store and validate locally.
//
// Spec anchor: ADR-027.
type ArtifactStore interface {
	Put(ctx context.Context, meta Artifact, content io.Reader) (Artifact, error)
	Get(ctx context.Context, id string) (io.ReadCloser, Artifact, error)
	Describe(ctx context.Context, id string) (Artifact, error)
	List(ctx context.Context, sel ArtifactSelector) ([]Artifact, error)
}
