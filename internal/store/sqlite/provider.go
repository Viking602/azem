package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io"
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
	if !memory {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
		if err := backupExisting(path); err != nil {
			return nil, err
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
		`PRAGMA journal_mode = WAL`,
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure sqlite: %w", err)
		}
	}
	if err := migrate(ctx, db); err != nil {
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

func backupExisting(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect database: %w", err)
	}
	if info.Size() == 0 {
		return nil
	}
	source, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open database backup source: %w", err)
	}
	defer source.Close()
	backupPath := path + ".bak"
	target, err := os.OpenFile(backupPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create database backup: %w", err)
	}
	_, copyErr := io.Copy(target, source)
	closeErr := target.Close()
	if copyErr != nil {
		return fmt.Errorf("copy database backup: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close database backup: %w", closeErr)
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
