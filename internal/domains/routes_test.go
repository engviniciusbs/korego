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
