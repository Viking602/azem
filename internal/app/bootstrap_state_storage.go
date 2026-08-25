package app

import (
	"github.com/Viking602/azem/internal/contextfiles"
	"github.com/Viking602/azem/internal/memory"
	"github.com/Viking602/azem/internal/rules"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

type bootstrapStorageState struct {
	store         *sqlitestore.Provider
	sessions      *session.Service
	memory        *memory.Service
	contextFiles  contextfiles.Result
	ruleResult    rules.Result
	ruleCatalog   *rules.Catalog
	recoveryFence sqlitestore.RecoveryFence
	shouldRecover bool
}
