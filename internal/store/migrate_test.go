package store

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpen_AppliesMigrations(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "k.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	for _, tbl := range []string{"users", "recovery_codes", "sessions", "audit_log", "schema_migrations"} {
		var n int
		require.NoError(t, db.SQL().QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&n))
		require.Equal(t, 1, n, tbl)
	}
	var v int
	require.NoError(t, db.SQL().QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&v))
	require.Equal(t, 1, v)
}

func TestOpen_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k.db")
	db1, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, db1.Close())
	db2, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db2.Close() })

	var n int
	require.NoError(t, db2.SQL().QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n))
	require.Equal(t, 1, n)
}

func TestOpen_ForeignKeysOn(t *testing.T) {
	db := OpenMemory(t)
	var fk int
	require.NoError(t, db.SQL().QueryRow(`PRAGMA foreign_keys`).Scan(&fk))
	require.Equal(t, 1, fk)
}
