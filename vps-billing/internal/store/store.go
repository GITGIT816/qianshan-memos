// Package store persists plans, customers, and subscriptions to SQLite.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers "sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS plans (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	name          TEXT NOT NULL UNIQUE,
	price_cents   INTEGER NOT NULL,
	traffic_bytes INTEGER NOT NULL,
	duration_days INTEGER NOT NULL,
	device_limit  INTEGER NOT NULL,
	created_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS customers (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	name       TEXT NOT NULL,
	contact    TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS subscriptions (
	id                  INTEGER PRIMARY KEY AUTOINCREMENT,
	customer_id         INTEGER NOT NULL REFERENCES customers(id),
	plan_id             INTEGER NOT NULL REFERENCES plans(id),
	email               TEXT NOT NULL UNIQUE,
	uuid                TEXT NOT NULL,
	inbound_tag         TEXT NOT NULL,
	status              TEXT NOT NULL,
	suspend_reason      TEXT NOT NULL DEFAULT '',
	traffic_limit_bytes INTEGER NOT NULL,
	traffic_used_bytes  INTEGER NOT NULL DEFAULT 0,
	device_limit        INTEGER NOT NULL,
	last_seen_devices   INTEGER NOT NULL DEFAULT 0,
	starts_at           INTEGER NOT NULL,
	expires_at          INTEGER NOT NULL,
	created_at          INTEGER NOT NULL,
	updated_at          INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_subscriptions_status ON subscriptions(status);
`

// Store wraps a SQLite connection with the billing schema applied.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and applies the schema.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite only tolerates one writer at a time; serialize access from this
	// process rather than fighting "database is locked" errors under the
	// CLI + background sync loop both touching the file.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}
