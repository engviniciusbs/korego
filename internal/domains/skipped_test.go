package domains

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/store"
)

func TestSitesReport_ListsSkippedWithReasons(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t)
	noPort := mkApp(t, s.DB, "nova", 0)
	ins := func(d store.Domain) {
		require.NoError(t, s.DB.InsertDomain(ctx, &d))
	}
	ins(store.Domain{Hostname: "exemplo.com", TargetType: TargetAddress, Address: "127.0.0.1:5000", WWWRedirect: "www_to_apex"})
	ins(store.Domain{Hostname: "www.exemplo.com", TargetType: TargetAddress, Address: "127.0.0.1:5001", WWWRedirect: "none"})
	ins(store.Domain{Hostname: "nova.exemplo.com", TargetType: TargetApp, AppID: &noPort.ID, WWWRedirect: "none"})
	ins(store.Domain{Hostname: "loja.exemplo.net", TargetType: TargetAddress, Address: "127.0.0.1:5002",
		WWWRedirect: "none", TLSMode: proxy.TLSCustom}) // sem certificado escolhido

	sites, skipped, err := s.SitesReport(ctx)
	require.NoError(t, err)
	require.Len(t, sites, 1)
	assert.Equal(t, "exemplo.com", sites[0].Host)

	hosts := map[string]string{}
	for _, sk := range skipped {
		hosts[sk.Host] = sk.Reason
	}
	require.Len(t, hosts, 3, "%+v", skipped)
	assert.Contains(t, hosts["www.exemplo.com"], "já é usado pelo domínio exemplo.com")
	assert.Contains(t, hosts["nova.exemplo.com"], "app sem porta")
	assert.Contains(t, hosts["loja.exemplo.net"], "certificado próprio")

	// Sites devolve os mesmos sites.
	plain, err := s.Sites(ctx)
	require.NoError(t, err)
	assert.Equal(t, sites, plain)
}
