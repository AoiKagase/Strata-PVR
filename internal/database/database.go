package database

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

const CurrentSchemaVersion = 6

type handleContextKey struct{}

// WithHandle makes a process-owned database handle available to repository calls.
func WithHandle(ctx context.Context, db *sql.DB) context.Context {
	if db == nil {
		return ctx
	}
	return context.WithValue(ctx, handleContextKey{}, db)
}

// Acquire reuses a process-owned handle when present, otherwise opening a
// short-lived handle for compatibility with standalone repository calls.
func Acquire(ctx context.Context, path string) (*sql.DB, func(), error) {
	if db, ok := ctx.Value(handleContextKey{}).(*sql.DB); ok && db != nil {
		return db, func() {}, nil
	}
	db, err := Open(ctx, path)
	if err != nil {
		return nil, nil, err
	}
	return db, func() { _ = db.Close() }, nil
}

func Open(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := initialize(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func initialize(ctx context.Context, db *sql.DB) error {
	for _, statement := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		)`,
		`CREATE TABLE IF NOT EXISTS rules (
			id INTEGER PRIMARY KEY,
			position INTEGER NOT NULL UNIQUE CHECK (position >= 0),
			document_json TEXT NOT NULL CHECK (json_valid(document_json)),
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		)`,
		`CREATE TABLE IF NOT EXISTS reservations (
			id INTEGER PRIMARY KEY,
			program_id TEXT NOT NULL UNIQUE,
			position INTEGER NOT NULL UNIQUE CHECK (position >= 0),
			start_at INTEGER NOT NULL DEFAULT 0,
			end_at INTEGER NOT NULL DEFAULT 0,
			document_json TEXT NOT NULL CHECK (json_valid(document_json)),
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		)`,
		`CREATE INDEX IF NOT EXISTS reservations_time_idx ON reservations(start_at, end_at)`,
		`CREATE TABLE IF NOT EXISTS schedule_channels (
			id INTEGER PRIMARY KEY,
			channel_key TEXT NOT NULL UNIQUE,
			position INTEGER NOT NULL UNIQUE CHECK (position >= 0),
			document_json TEXT NOT NULL CHECK (json_valid(document_json))
		)`,
		`CREATE TABLE IF NOT EXISTS schedule_programs (
			id INTEGER PRIMARY KEY,
			program_id TEXT NOT NULL UNIQUE,
			channel_key TEXT NOT NULL,
			position INTEGER NOT NULL CHECK (position >= 0),
			start_at INTEGER NOT NULL,
			end_at INTEGER NOT NULL,
			document_json TEXT NOT NULL CHECK (json_valid(document_json)),
			FOREIGN KEY (channel_key) REFERENCES schedule_channels(channel_key) ON DELETE CASCADE,
			UNIQUE (channel_key, position)
		)`,
		`CREATE INDEX IF NOT EXISTS schedule_programs_channel_time_idx ON schedule_programs(channel_key, start_at, end_at)`,
		`CREATE INDEX IF NOT EXISTS schedule_programs_time_idx ON schedule_programs(start_at, end_at)`,
		`CREATE TABLE IF NOT EXISTS program_collections (
			id INTEGER PRIMARY KEY,
			collection TEXT NOT NULL CHECK (collection IN ('recording', 'recorded')),
			program_id TEXT NOT NULL,
			position INTEGER NOT NULL CHECK (position >= 0),
			start_at INTEGER NOT NULL DEFAULT 0,
			end_at INTEGER NOT NULL DEFAULT 0,
			document_json TEXT NOT NULL CHECK (json_valid(document_json)),
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			UNIQUE (collection, program_id),
			UNIQUE (collection, position)
		)`,
		`CREATE INDEX IF NOT EXISTS program_collections_time_idx ON program_collections(collection, start_at, end_at)`,
		`CREATE TABLE IF NOT EXISTS preview_cache (
			cache_key TEXT PRIMARY KEY,
			program_id TEXT NOT NULL,
			source_path TEXT NOT NULL,
			source_size INTEGER NOT NULL,
			source_mtime INTEGER NOT NULL,
			file_name TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
			accessed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		)`,
		`CREATE INDEX IF NOT EXISTS preview_cache_program_idx ON preview_cache(program_id)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize SQLite: %w", err)
		}
	}
	_, err := db.ExecContext(ctx, "INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)", CurrentSchemaVersion)
	return err
}
