package app

import (
	"path/filepath"

	backgroundservice "github.com/Viking602/azem/internal/background"
)

func (b *bootstrapAssembly) attachBackground() error {
	manager, err := backgroundservice.NewManager(backgroundservice.Options{
		Root: b.paths.Workspace, LogDir: filepath.Join(b.paths.StateDir, "background"),
	})
	if err != nil {
		return err
	}
	b.service.AttachBackground(manager)
	return nil
}
