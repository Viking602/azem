package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/Viking602/go-hydaelyn/api"
	_ "modernc.org/sqlite"
)

var memoryCounter atomic.Uint64

type Provider struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Provider, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite path is empty")
	}
	memory := path == ":memory:"
	existed := false
	if !memory {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
		info, err := os.Stat(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("inspect database: %w", err)
		}
		if err == nil && info.Size() > 0 {
			existed = true
		}
	}
	dsn := sqliteDSN(path, memory)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if memory {
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(8)
	}
	db.SetMaxIdleConns(2)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	for _, pragma := range []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure sqlite: %w", err)
		}
	}
	upgrade := func() error {
		if _, err := db.ExecContext(ctx, `PRAGMA journal_mode = WAL`); err != nil {
			return fmt.Errorf("configure sqlite journal: %w", err)
		}
		version, err := currentSchemaVersion(ctx, db)
		if err != nil {
			return err
		}
		if version > schemaVersion {
			return fmt.Errorf("database schema %d is newer than supported schema %d", version, schemaVersion)
		}
		if existed && version < schemaVersion {
			if err := backupDatabase(ctx, db, path); err != nil {
				return err
			}
		}
		return migrate(ctx, db)
	}
	if !memory {
		err = withDatabaseUpgradeLock(ctx, path, upgrade)
	} else {
		err = upgrade()
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	if !memory {
		if err := os.Chmod(path, 0o600); err != nil {
			db.Close()
			return nil, fmt.Errorf("protect database: %w", err)
		}
	}
	return &Provider{db: db}, nil
}

func (p *Provider) Begin(ctx context.Context) (api.UnitOfWork, error) {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("begin sqlite unit of work: %w", err)
	}
	return &unitOfWork{tx: tx}, nil
}

func (p *Provider) Capabilities(context.Context) (api.StoreCapabilities, error) {
	return api.StoreCapabilities{
		SupportsTransactions:        true,
		SupportsBlackboardSubscribe: false,
		SupportsListPending:         true,
		SupportsConcurrentWriters:   true,
		SupportsDeadLetterRequeue:   false,
	}, nil
}

func (p *Provider) Checkpoint(ctx context.Context) error {
	if _, err := p.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("checkpoint sqlite WAL: %w", err)
	}
	return nil
}

func (p *Provider) Close(context.Context) error {
	return p.db.Close()
}

func (p *Provider) DB() *sql.DB { return p.db }

func sqliteDSN(path string, memory bool) string {
	pragmas := "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate"
	if memory {
		return fmt.Sprintf("file:azem-%d?mode=memory&cache=shared&%s", memoryCounter.Add(1), pragmas)
	}
	return "file:" + filepath.ToSlash(path) + "?" + pragmas
}

func backupDatabase(ctx context.Context, db *sql.DB, path string) error {
	backupPath := path + ".bak"
	temporary, err := os.CreateTemp(filepath.Dir(path), filepath.Base(backupPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create database backup temporary path: %w", err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close database backup temporary path: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("prepare database backup temporary path: %w", err)
	}
	defer os.Remove(temporaryPath)
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, temporaryPath); err != nil {
		return fmt.Errorf("create consistent database backup: %w", err)
	}
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return fmt.Errorf("protect database backup: %w", err)
	}
	if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("replace database backup: %w", err)
	}
	if err := os.Rename(temporaryPath, backupPath); err != nil {
		return fmt.Errorf("publish database backup: %w", err)
	}
	return nil
}

func isBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked") || strings.Contains(message, "sqlite_busy")
}

var (
	_ api.StoreProvider      = (*Provider)(nil)
	_ api.CapabilityReporter = (*Provider)(nil)
	_ api.ProviderCloser     = (*Provider)(nil)
)
