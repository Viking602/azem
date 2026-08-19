package observation

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/Viking602/azem/internal/session"
)

// ObservationMetadata contains only facts known by the host but not retained
// directly on a ToolRecord. Nil booleans mean unknown, never false.
type ObservationMetadata struct {
	ExitStatus   *int
	TimedOut     bool
	Cancelled    bool
	RawAvailable *bool
	Truncated    *bool
	Spilled      *bool
	Retryable    *bool
	FreshAt      time.Time
}

// NormalizeToolObservation wraps a durable tool record with reliability
// metadata. It never rewrites Content, Structured, Arguments, or file evidence.
func NormalizeToolObservation(record session.ToolRecord, metadata ObservationMetadata) session.ObservationEnvelopeV1 {
	freshAt := record.CompletedAt
	if freshAt.IsZero() {
		freshAt = metadata.FreshAt
	}
	presence := "empty"
	rawAvailable := true
	if metadata.RawAvailable != nil {
		rawAvailable = *metadata.RawAvailable
	}
	if !rawAvailable {
		presence = "missing"
	} else if record.Content != "" || hasStructuredContent(record.Structured) {
		presence = "present"
	}
	truncation := "unknown"
	if metadata.Spilled != nil && *metadata.Spilled {
		truncation = "spilled"
	} else if metadata.Truncated != nil && *metadata.Truncated {
		truncation = "truncated"
	} else if metadata.Spilled != nil && metadata.Truncated != nil {
		truncation = "none"
	}
	retryability := "unknown"
	if metadata.Retryable != nil {
		if *metadata.Retryable {
			retryability = "retryable"
		} else {
			retryability = "terminal"
		}
	}
	rawHash := rawToolResultHash(record.Content, record.Structured)
	recordID := strings.Join([]string{record.SessionID, record.RunID, record.ToolCallID}, ":")
	rawRef := session.SourceRefV1{Kind: "tool_record", ID: recordID, SHA256: rawHash}
	envelope := session.ObservationEnvelopeV1{
		Version:         session.WorkContractVersionV1,
		ID:              "observation:" + recordID,
		SessionID:       record.SessionID,
		RunID:           record.RunID,
		ToolCallID:      record.ToolCallID,
		Status:          normalizeToolStatus(record.State, metadata),
		ExitStatus:      cloneInt(metadata.ExitStatus),
		ContentPresence: presence,
		Truncation:      truncation,
		SchemaValidity:  structuredValidity(record.Structured),
		Retryability:    retryability,
		FreshAt:         freshAt,
		RawSHA256:       rawHash,
		RawRef:          &rawRef,
		Files:           normalizeFileObservations(record.Observations),
		Sources:         []session.SourceRefV1{rawRef},
	}
	if record.ArtifactID != "" {
		envelope.Sources = append(envelope.Sources, session.SourceRefV1{Kind: "artifact", ID: record.ArtifactID})
	}
	for _, observation := range record.Observations {
		if observation.Path != "" {
			envelope.Sources = append(envelope.Sources, session.SourceRefV1{Kind: "file", ID: observation.Path, SHA256: observation.SHA256})
		}
	}
	return envelope
}

func normalizeToolStatus(state string, metadata ObservationMetadata) string {
	if metadata.TimedOut {
		return "timeout"
	}
	if metadata.Cancelled {
		return "cancelled"
	}
	switch state {
	case session.ToolCompleted:
		return "completed"
	case session.ToolFailed:
		return "failed"
	case session.ToolInterrupted:
		return "cancelled"
	default:
		return "unknown"
	}
}

func hasStructuredContent(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "{}" && trimmed != "[]"
}

func structuredValidity(raw json.RawMessage) string {
	if len(strings.TrimSpace(string(raw))) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return "unknown"
	}
	if json.Valid(raw) {
		return "valid"
	}
	return "invalid"
}

func normalizeFileObservations(observations []session.FileObservation) []session.FileObservationV1 {
	result := make([]session.FileObservationV1, 0, len(observations))
	for _, observation := range observations {
		file := session.FileObservationV1{Path: observation.Path, State: "unknown"}
		switch observation.Operation {
		case "read":
			file.State, file.AfterSHA256 = "observed", observation.SHA256
		case "create", "created":
			file.State, file.AfterSHA256 = "created", observation.SHA256
		case "write", "edit", "modify", "modified":
			file.State, file.AfterSHA256 = "modified", observation.SHA256
		case "delete", "deleted":
			file.State, file.BeforeSHA256 = "deleted", observation.SHA256
		}
		result = append(result, file)
	}
	return result
}

func rawToolResultHash(content string, structured json.RawMessage) string {
	hash := sha256.New()
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(content)))
	hash.Write(size[:])
	hash.Write([]byte(content))
	binary.BigEndian.PutUint64(size[:], uint64(len(structured)))
	hash.Write(size[:])
	hash.Write(structured)
	return hex.EncodeToString(hash.Sum(nil))
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
