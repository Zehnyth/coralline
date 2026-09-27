// Package sqlitestore is the repository layer: the only place that talks
// SQL. It is deliberately CQRS-lite (see PRD §6.2) — simple reads are
// plain queries here with no service-layer indirection above them; only
// operations that coordinate with blob storage (publish) get a dedicated
// coordinator, which lives in package modules and calls back into this
// repository.
package sqlitestore

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, no cgo — registers as "sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type DB struct {
	sql *sql.DB
}

func Open(path string) (*DB, error) {
	// modernc.org/sqlite DSN pragmas: WAL mode + a busy timeout so a
	// concurrent writer doesn't fail fast with SQLITE_BUSY under normal
	// load (see PRD FR-6.2 / SR-3.3-adjacent write contention notes).
	// foreign_keys is per-connection in SQLite and off by default; the DSN
	// pragma applies it to every pooled connection so token_grants and
	// token_tags really do cascade with their token.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	// SQLite has a single writer; keep the pool small and predictable.
	sqlDB.SetMaxOpenConns(8)

	db := &DB{sql: sqlDB}
	if err := db.migrate(context.Background()); err != nil {
		return nil, fmt.Errorf("migrating database: %w", err)
	}
	if err := db.startCheckpointLoop(context.Background()); err != nil {
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.sql.Close()
}

// Ping backs the /readyz endpoint (PRD FR-9.2).
func (db *DB) Ping(ctx context.Context) error {
	return db.sql.PingContext(ctx)
}

// migrate applies embedded, append-only migrations tracked via
// PRAGMA user_version — the only supported upgrade path (PRD FR-6.11).
// Deleting the database is never a valid update procedure.
func (db *DB) migrate(ctx context.Context) error {
	var current int
	if err := db.sql.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return err
	}

	migrations := []struct {
		version int
		file    string
	}{
		{1, "migrations/0001_init.sql"},
		{2, "migrations/0002_tokens_v2.sql"},
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		b, err := migrationsFS.ReadFile(m.file)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", m.file, err)
		}
		tx, err := db.sql.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(b)); err != nil {
			tx.Rollback()
			return fmt.Errorf("applying migration %s: %w", m.file, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// startCheckpointLoop runs a periodic passive WAL checkpoint so a
// long-held read transaction cannot grow the WAL file without bound
// (PRD FR-6.2).
func (db *DB) startCheckpointLoop(ctx context.Context) error {
	if _, err := db.sql.ExecContext(ctx, "PRAGMA wal_autocheckpoint = 1000"); err != nil {
		return err
	}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			_, _ = db.sql.Exec("PRAGMA wal_checkpoint(PASSIVE)")
		}
	}()
	return nil
}
