package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDomains_CRUDAndConflict(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()

	d := &Domain{Hostname: "b.exemplo.com", TargetType: "address", Address: "127.0.0.1:9000",
		WWWRedirect: "none", ForceHTTPS: true}
	require.NoError(t, db.InsertDomain(ctx, d))
	require.NotZero(t, d.ID)
	require.False(t, d.CreatedAt.IsZero())

	a := &Domain{Hostname: "a.exemplo.com", TargetType: "address", Address: "127.0.0.1:9001", WWWRedirect: "none"}
	require.NoError(t, db.InsertDomain(ctx, a))

	got, err := db.GetDomain(ctx, d.ID)
	require.NoError(t, err)
	require.Equal(t, "b.exemplo.com", got.Hostname)
	require.True(t, got.ForceHTTPS)
	require.False(t, got.IsPanel)
	require.Nil(t, got.AppID)

	got.Address = "127.0.0.1:9100"
	got.ForceHTTPS = false
	got.WWWRedirect = "www_to_apex"
	require.NoError(t, db.UpdateDomain(ctx, &got))
	again, err := db.GetDomain(ctx, d.ID)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:9100", again.Address)
	require.False(t, again.ForceHTTPS)
	require.Equal(t, "www_to_apex", again.WWWRedirect)

	list, err := db.ListDomains(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, "a.exemplo.com", list[0].Hostname)

	dup := &Domain{Hostname: "a.exemplo.com", TargetType: "address", Address: "x", WWWRedirect: "none"}
	require.ErrorIs(t, db.InsertDomain(ctx, dup), ErrConflict)

	require.NoError(t, db.DeleteDomain(ctx, d.ID))
	require.ErrorIs(t, db.DeleteDomain(ctx, d.ID), ErrNotFound)
	_, err = db.GetDomain(ctx, d.ID)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, db.UpdateDomain(ctx, &got), ErrNotFound)
}

func TestDomains_PanelAndByApp(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()

	_, ok, err := db.PanelDomain(ctx)
	require.NoError(t, err)
	require.False(t, ok)

	now := time.Now().Unix()
	res, err := db.SQL().Exec(`INSERT INTO projects (name, created_at) VALUES ('p', ?)`, now)
	require.NoError(t, err)
	pid, _ := res.LastInsertId()
	res, err = db.SQL().Exec(`INSERT INTO apps (project_id, slug, source_type, build_type, internal_port, created_at, updated_at)
		VALUES (?, 'app1', 'image', 'image', 80, ?, ?)`, pid, now, now)
	require.NoError(t, err)
	appID, _ := res.LastInsertId()

	require.NoError(t, db.InsertDomain(ctx, &Domain{Hostname: "painel.exemplo.com", TargetType: "address",
		Address: "127.0.0.1:8080", WWWRedirect: "none", ForceHTTPS: true, IsPanel: true}))
	require.NoError(t, db.InsertDomain(ctx, &Domain{Hostname: "site.exemplo.com", TargetType: "app",
		AppID: &appID, WWWRedirect: "none", ForceHTTPS: true}))
	require.NoError(t, db.InsertDomain(ctx, &Domain{Hostname: "outro.exemplo.com", TargetType: "address",
		Address: "127.0.0.1:1", WWWRedirect: "none"}))

	p, ok, err := db.PanelDomain(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "painel.exemplo.com", p.Hostname)
	require.True(t, p.IsPanel)

	by, err := db.DomainsByApp(ctx, appID)
	require.NoError(t, err)
	require.Len(t, by, 1)
	require.Equal(t, "site.exemplo.com", by[0].Hostname)
	require.NotNil(t, by[0].AppID)
	require.Equal(t, appID, *by[0].AppID)

	none, err := db.DomainsByApp(ctx, appID+99)
	require.NoError(t, err)
	require.Empty(t, none)
}
