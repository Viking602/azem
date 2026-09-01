package contextarchive

import (
	"encoding/json"

	"github.com/Viking602/azem/internal/agentruntime"
)

type sourceV1Wire struct {
	Version  int                             `json:"version"`
	Messages []agentruntime.PersistedMessage `json:"messages"`
}

func (source SourceV1) MarshalJSON() ([]byte, error) {
	return json.Marshal(sourceV1Wire{Version: source.Version, Messages: agentruntime.PersistMessages(source.Messages)})
}

func (source *SourceV1) UnmarshalJSON(encoded []byte) error {
	var wire sourceV1Wire
	if err := json.Unmarshal(encoded, &wire); err != nil {
		return err
	}
	source.Version = wire.Version
	source.Messages = agentruntime.RuntimeMessages(wire.Messages)
	return nil
}
