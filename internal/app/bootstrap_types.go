package app

import (
	"context"

	"github.com/Viking602/azem/internal/config"
)

type bootstrapAssembly struct {
	ctx              context.Context
	cfg              config.Config
	paths            config.Paths
	homeDir          string
	configDir        string
	startupSessionID string

	bootstrapStorageState
	bootstrapExtensionState
	bootstrapRuntimeState
}
