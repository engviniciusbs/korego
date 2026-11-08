package domains

import (
	"context"
	"testing"

	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutes_AppAddressPanel(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t)
	withPort := mkApp(t, s.DB, "web", 20001)
	noPort := mkApp(t, s.DB, "nova", 0)

	ins := func(d store.Domain) {
		require.NoError(t, s.DB.InsertDomain(ctx, &d))
	}
	ins(store.Domain{Hostname: "app.exemplo.com", TargetType: TargetApp, AppID: &withPort.ID,
		WWWRedirect: "none", ForceHTTPS: true})
	ins(store.Domain{Hostname: "nova.exemplo.com", TargetType: TargetApp, AppID: &noPort.ID, WWWRedirect: "none"})
	ins(store.Domain{Hostname: "exemplo.com", TargetType: TargetAddress, Address: "127.0.0.1:5000",
		WWWRedirect: "www_to_apex"})
	ins(store.Domain{Hostname: "painel.exemplo.com", TargetType: TargetAddress, IsPanel: true,
		WWWRedirect: "none", ForceHTTPS: true})

	routes, err := s.Routes(ctx)
	require.NoError(t, err)
	assert.Equal(t, []proxy.Route{
		{Host: "app.exemplo.com", Upstream: "127.0.0.1:20001", ForceHTTPS: true, WWWRedirect: proxy.WWWNone},
		{Host: "exemplo.com", Upstream: "127.0.0.1:5000", WWWRedirect: proxy.WWWToApex},
		{Host: "painel.exemplo.com", Upstream: "127.0.0.1:8000", ForceHTTPS: true, WWWRedirect: proxy.WWWNone},
	}, routes)

	_, err = proxy.BuildConfig(routes)
	assert.NoError(t, err)
}

func TestRoutes_SkipsConflictingRows(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t)
	ins := func(d store.Domain) {
		require.NoError(t, s.DB.InsertDomain(ctx, &d))
	}
	// exemplo.com com www_to_apex reserva www.exemplo.com; a linha
	// www.exemplo.com (gravada direto no banco) colide e fica de fora.
	ins(store.Domain{Hostname: "exemplo.com", TargetType: TargetAddress, Address: "127.0.0.1:5000", WWWRedirect: "www_to_apex"})
	ins(store.Domain{Hostname: "www.exemplo.com", TargetType: TargetAddress, Address: "127.0.0.1:5001", WWWRedirect: "none"})
	// O painel vence mesmo vindo depois na ordem por hostname.
	ins(store.Domain{Hostname: "zz.exemplo.com", TargetType: TargetAddress, Address: "127.0.0.1:5002", WWWRedirect: "www_to_apex"})
	ins(store.Domain{Hostname: "www.zz.exemplo.com", TargetType: TargetAddress, IsPanel: true, WWWRedirect: "none"})

	routes, err := s.Routes(ctx)
	require.NoError(t, err)
	assert.Equal(t, []proxy.Route{
		{Host: "exemplo.com", Upstream: "127.0.0.1:5000", WWWRedirect: proxy.WWWToApex},
		{Host: "www.zz.exemplo.com", Upstream: "127.0.0.1:8000", WWWRedirect: proxy.WWWNone},
	}, routes)
	_, err = proxy.BuildConfig(routes)
	assert.NoError(t, err)
}

func TestRoutes_SkipsInvalidWWWMode(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t)
	ins := func(d store.Domain) {
		require.NoError(t, s.DB.InsertDomain(ctx, &d))
	}
	ins(store.Domain{Hostname: "www.exemplo.com", TargetType: TargetAddress, Address: "127.0.0.1:5000", WWWRedirect: "www_to_apex"})
	ins(store.Domain{Hostname: "loja.exemplo.com", TargetType: TargetAddress, Address: "127.0.0.1:5001", WWWRedirect: "apex_to_www"})
	ins(store.Domain{Hostname: "x.exemplo.com", TargetType: TargetAddress, Address: "127.0.0.1:5002", WWWRedirect: "qualquer"})
	ins(store.Domain{Hostname: "ok.exemplo.com", TargetType: TargetAddress, Address: "127.0.0.1:5003", WWWRedirect: "none"})

	routes, err := s.Routes(ctx)
	require.NoError(t, err)
	assert.Equal(t, []proxy.Route{{Host: "ok.exemplo.com", Upstream: "127.0.0.1:5003", WWWRedirect: proxy.WWWNone}}, routes)
	_, err = proxy.BuildConfig(routes)
	assert.NoError(t, err)
}

func TestCheckCollisionExported(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t)
	d := store.Domain{Hostname: "exemplo.com", TargetType: TargetAddress, Address: "127.0.0.1:5000", WWWRedirect: "www_to_apex"}
	require.NoError(t, s.DB.InsertDomain(ctx, &d))
	err := s.CheckCollision(ctx, "www.exemplo.com", "none", 0)
	var inv *ErrInvalid
	require.ErrorAs(t, err, &inv)
	assert.Equal(t, "hostname", inv.Field)
	assert.NoError(t, s.CheckCollision(ctx, "exemplo.com", "www_to_apex", d.ID))
	assert.NoError(t, s.CheckCollision(ctx, "outro.exemplo.com", "none", 0))
}
