package session

import (
	"encoding/json"

	"github.com/Viking602/azem/internal/agentruntime"
)

type modelHistoryWire struct {
	ProviderID             string                          `json:"providerId,omitempty"`
	ModelID                string                          `json:"modelId,omitempty"`
	InstructionFingerprint string                          `json:"instructionFingerprint,omitempty"`
	Messages               []agentruntime.PersistedMessage `json:"messages,omitempty"`
	CoveredThroughSequence *int64                          `json:"coveredThroughSequence,omitempty"`
	Generation             int64                           `json:"generation,omitempty"`
	SummaryHash            string                          `json:"summaryHash,omitempty"`
	StaticPrefixHash       string                          `json:"staticPrefixHash,omitempty"`
	WireVersion            int                             `json:"wireVersion,omitempty"`
	ContextManifestHash    string                          `json:"contextManifestHash,omitempty"`
	PolicyVersion          int                             `json:"policyVersion,omitempty"`
}

func (history ModelHistory) MarshalJSON() ([]byte, error) {
	return json.Marshal(modelHistoryWire{
		ProviderID: history.ProviderID, ModelID: history.ModelID,
		InstructionFingerprint: history.InstructionFingerprint,
		Messages:               agentruntime.PersistMessages(history.Messages),
		CoveredThroughSequence: history.CoveredThroughSequence,
		Generation:             history.Generation, SummaryHash: history.SummaryHash,
		StaticPrefixHash: history.StaticPrefixHash, WireVersion: history.WireVersion,
		ContextManifestHash: history.ContextManifestHash, PolicyVersion: history.PolicyVersion,
	})
}

func (history *ModelHistory) UnmarshalJSON(encoded []byte) error {
	var wire modelHistoryWire
	if err := json.Unmarshal(encoded, &wire); err != nil {
		return err
	}
	*history = ModelHistory{
		ProviderID: wire.ProviderID, ModelID: wire.ModelID,
		InstructionFingerprint: wire.InstructionFingerprint,
		Messages:               agentruntime.RuntimeMessages(wire.Messages),
		CoveredThroughSequence: wire.CoveredThroughSequence,
		Generation:             wire.Generation, SummaryHash: wire.SummaryHash,
		StaticPrefixHash: wire.StaticPrefixHash, WireVersion: wire.WireVersion,
		ContextManifestHash: wire.ContextManifestHash, PolicyVersion: wire.PolicyVersion,
	}
	return nil
}
