package app

import (
	"context"
	"encoding/json"
	"strings"

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
	extensionCommands := "[]"
	if s.extensionHost != nil {
		encodedExtensionCommands, encodeErr := json.Marshal(s.extensionHost.ExtensionCommands())
		if encodeErr != nil {
			return encodeErr
		}
		extensionCommands = string(encodedExtensionCommands)
	}
	s.emit(ctx, Event{Kind: EventCommandCatalog, State: state, Data: map[string]string{
		"commands": string(encoded), "extensionCommands": extensionCommands, "diagnostics": string(diagnostics),
	}})
	return nil
}

func extensionCommandInput(input string) (string, string, bool) {
	if !strings.HasPrefix(input, "/") {
		return "", "", false
	}
	value := strings.TrimPrefix(input, "/")
	name, arguments, found := strings.Cut(value, " ")
	if !found {
		name, arguments, found = strings.Cut(value, "\t")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", false
	}
	return name, strings.TrimSpace(arguments), true
}
