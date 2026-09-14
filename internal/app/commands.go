package app

import (
	"context"
	"encoding/json"

	"github.com/Viking602/azem/internal/commands"
)

func (s *Service) emitCommandCatalog(ctx context.Context, state string) error {
	entries := s.commandCatalog.Entries()
	if entries == nil {
		entries = []commands.Entry{}
	}
	diagnosticEntries := append([]string(nil), s.commandDiagnostics...)
	if diagnosticEntries == nil {
		diagnosticEntries = []string{}
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	diagnostics, err := json.Marshal(diagnosticEntries)
	if err != nil {
		return err
	}
	s.emit(ctx, Event{Kind: EventCommandCatalog, State: state, Data: map[string]string{
		"commands": string(encoded), "diagnostics": string(diagnostics),
	}})
	return nil
}
