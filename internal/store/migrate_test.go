package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpen_AppliesMigrations(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "k.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	for _, tbl := range []string{"users", "recovery_codes", "sessions", "audit_log", "schema_migrations", "metrics_minute", "settings", "projects", "apps", "app_env", "deploys", "domains", "github_app", "usage_minute", "certificates", "domain_routes", "api_tokens"} {
		var n int
		require.NoError(t, db.SQL().QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&n))
		require.Equal(t, 1, n, tbl)
	}
	var v int
	require.NoError(t, db.SQL().QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&v))
	require.Equal(t, 6, v)
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
	require.Equal(t, 6, n)
}

func TestOpen_ForeignKeysOn(t *testing.T) {
	db := OpenMemory(t)
	var fk int
	require.NoError(t, db.SQL().QueryRow(`PRAGMA foreign_keys`).Scan(&fk))
	require.Equal(t, 1, fk)
}

func TestMigrate_0005_ConvertsDomainsToRootRoute(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "m.db")+"?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	_, err = sqlDB.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`)
	require.NoError(t, err)

	for v, name := range []string{"0001_init.sql", "0002_metrics.sql", "0003_publish.sql", "0004_usage.sql"} {
		body, err := migrationsFS.ReadFile("migrations/" + name)
		require.NoError(t, err)
		require.NoError(t, applyMigration(sqlDB, v+1, string(body)))
	}
	_, err = sqlDB.Exec(`INSERT INTO projects(id, name, created_at) VALUES (1, 'p', 1)`)
	require.NoError(t, err)
	_, err = sqlDB.Exec(`INSERT INTO apps(id, project_id, slug, source_type, build_type, internal_port, created_at, updated_at)
		VALUES (1, 1, 'loja', 'image', 'image', 80, 1, 1)`)
	require.NoError(t, err)
	_, err = sqlDB.Exec(`INSERT INTO domains(id, hostname, target_type, app_id, address, created_at, updated_at)
		VALUES (1, 'app.exemplo.com', 'app', 1, '', 1, 1), (2, 'api.exemplo.com', 'address', NULL, '127.0.0.1:9000', 1, 1)`)
	require.NoError(t, err)

	body, err := migrationsFS.ReadFile("migrations/0005_proxy.sql")
	require.NoError(t, err)
	require.NoError(t, applyMigration(sqlDB, 5, string(body)))

	type route struct {
		path, typ, addr string
		app             sql.NullInt64
	}
	get := func(id int) route {
		rows, err := sqlDB.Query(`SELECT path, target_type, app_id, address FROM domain_routes WHERE domain_id = ?`, id)
		require.NoError(t, err)
		defer rows.Close()
		var rs []route
		for rows.Next() {
			var r route
			require.NoError(t, rows.Scan(&r.path, &r.typ, &r.app, &r.addr))
			rs = append(rs, r)
		}
		require.Len(t, rs, 1)
		return rs[0]
	}
	r1 := get(1)
	require.Equal(t, "/", r1.path)
	require.Equal(t, "app", r1.typ)
	require.Equal(t, int64(1), r1.app.Int64)
	r2 := get(2)
	require.Equal(t, "/", r2.path)
	require.Equal(t, "address", r2.typ)
	require.False(t, r2.app.Valid)
	require.Equal(t, "127.0.0.1:9000", r2.addr)

	var comp int
	var tls string
	require.NoError(t, sqlDB.QueryRow(`SELECT compression, tls_mode FROM domains WHERE id = 1`).Scan(&comp, &tls))
	require.Equal(t, 0, comp)
	require.Equal(t, "auto", tls)
}
