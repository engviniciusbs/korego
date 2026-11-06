// Package store provides the SQLite persistence layer.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // driver "sqlite" (pure Go, no CGO)
)

// DB wraps the SQLite connection pool.
type DB struct {
	sql *sql.DB
}

// Open opens (creating if needed) the database at path and applies migrations.
// Pragmas are set via DSN so they apply to every pooled connection.
func Open(path string) (*DB, error) {
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("abrir banco: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("conectar ao banco: %w", err)
	}
	if err := migrate(sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return &DB{sql: sqlDB}, nil
}

// Close closes the underlying pool.
func (d *DB) Close() error { return d.sql.Close() }

// SQL returns the underlying *sql.DB.
func (d *DB) SQL() *sql.DB { return d.sql }
