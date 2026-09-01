package app

import (
	"time"

	"github.com/Viking602/azem/internal/memory"
	"github.com/Viking602/azem/internal/rules"
	"github.com/Viking602/azem/internal/session"
	sqlitestore "github.com/Viking602/azem/internal/store/sqlite"
)

type bootstrapStorageState struct {
	store                       *sqlitestore.Provider
	sessions                    *session.Service
	memory                      *memory.Service
	ruleResult                  rules.Result
	ruleCatalog                 *rules.Catalog
	recoveryFence               sqlitestore.RecoveryFence
	shouldRecover               bool
	recoveryPrepared            bool
	recoveryPreparedAt          time.Time
	recoveryExpiredLeases       int64
	recoveryQuarantinedAttempts int64
}
