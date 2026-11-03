package store

import (
	"path/filepath"
	"testing"
)

// OpenMemory opens a fresh, migrated database in t.TempDir() for tests.
// Despite the name it is file-backed so every pooled connection shares state.
// The DB is closed before TempDir cleanup (required on Windows).
func OpenMemory(t testing.TB) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "korego-test.db"))
	if err != nil {
		t.Fatalf("abrir banco de teste: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
