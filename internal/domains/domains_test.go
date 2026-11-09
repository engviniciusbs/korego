package domains

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func addrInput(host string) DomainInput {
	return DomainInput{Hostname: host, TargetType: TargetAddress, Address: "127.0.0.1:5000",
		WWWRedirect: "none", ForceHTTPS: true}
}

func TestCreate_SavesEvenWhenProxyBlocked(t *testing.T) {
	ctx := context.Background()
	s, fake := newTestService(t)
	fake.ApplyFn = func(context.Context, []proxy.Site, proxy.Listen, []string) error { return proxy.ErrInactive }

	d, dns, err := s.Create(ctx, addrInput("exemplo.com"))
	require.NoError(t, err)
	assert.NotZero(t, d.ID)
	assert.True(t, dns.OK)

	got, err := s.DB.GetDomain(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, "exemplo.com", got.Hostname)
	assert.Equal(t, "127.0.0.1:5000", got.Address)
	assert.Nil(t, got.AppID)
}

func TestCreate_OtherApplyErrorStillSaves(t *testing.T) {
	ctx := context.Background()
	s, fake := newTestService(t)
	fake.ApplyFn = func(context.Context, []proxy.Site, proxy.Listen, []string) error { return errors.New("caddy fora") }
	d, _, err := s.Create(ctx, addrInput("exemplo.com"))
	require.NoError(t, err)
	_, err = s.DB.GetDomain(ctx, d.ID)
	require.NoError(t, err)
}

func TestCreate_AppliesRoutes(t *testing.T) {
	ctx := context.Background()
	s, fake := newTestService(t)
	app := mkApp(t, s.DB, "web", 20001)
	d, _, err := s.Create(ctx, DomainInput{Hostname: "app.exemplo.com", TargetType: TargetApp,
		AppID: &app.ID, Address: "ignorado:1", ForceHTTPS: true})
	require.NoError(t, err)
	assert.Equal(t, "none", d.WWWRedirect) // vazio vira none
	assert.Empty(t, d.Address)             // endereço não se aplica a app
	assert.Equal(t, []proxy.Site{{Host: "app.exemplo.com",
		ForceHTTPS: true, WWWRedirect: proxy.WWWNone, TLS: autoTLS, Compression: true, Routes: []proxy.SiteRoute{{Path: "/", Upstream: "127.0.0.1:20001"}}}}, fake.LastApplied())
}

func invalidField(t *testing.T, err error) string {
	t.Helper()
	var inv *ErrInvalid
	require.True(t, errors.As(err, &inv), "esperava ErrInvalid, veio %v", err)
	assert.NotEmpty(t, inv.Message)
	return inv.Field
}

func TestCreate_InvalidAndPanelProtected(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t)
	app := mkApp(t, s.DB, "web", 20001)

	cases := []struct {
		name  string
		in    DomainInput
		field string
	}{
		{"hostname", addrInput("Exemplo.com"), "hostname"},
		{"tipo", DomainInput{Hostname: "exemplo.com", TargetType: "x"}, "targetType"},
		{"endereço", DomainInput{Hostname: "exemplo.com", TargetType: TargetAddress, Address: "127.0.0.1"}, "address"},
		{"app ausente", DomainInput{Hostname: "exemplo.com", TargetType: TargetApp}, "appId"},
		{"app inexistente", DomainInput{Hostname: "exemplo.com", TargetType: TargetApp, AppID: ptr(int64(999))}, "appId"},
		{"www desconhecido", DomainInput{Hostname: "exemplo.com", TargetType: TargetApp, AppID: &app.ID, WWWRedirect: "x"}, "wwwRedirect"},
		{"www_to_apex com www", DomainInput{Hostname: "www.exemplo.com", TargetType: TargetApp, AppID: &app.ID, WWWRedirect: "www_to_apex"}, "wwwRedirect"},
		{"apex_to_www sem www", DomainInput{Hostname: "exemplo.com", TargetType: TargetApp, AppID: &app.ID, WWWRedirect: "apex_to_www"}, "wwwRedirect"},
	}
	for _, c := range cases {
		_, _, err := s.Create(ctx, c.in)
		assert.Equal(t, c.field, invalidField(t, err), c.name)
	}
	list, err := s.DB.ListDomains(ctx)
	require.NoError(t, err)
	assert.Empty(t, list)

	// Hostname repetido → ErrConflict do store.
	_, _, err = s.Create(ctx, addrInput("exemplo.com"))
	require.NoError(t, err)
	_, _, err = s.Create(ctx, addrInput("exemplo.com"))
	assert.ErrorIs(t, err, ErrConflict)

	// Domínio do painel não muda por aqui.
	panel := store.Domain{Hostname: "painel.exemplo.com", TargetType: TargetAddress, IsPanel: true, WWWRedirect: "none"}
	require.NoError(t, s.DB.InsertDomain(ctx, &panel))
	_, _, err = s.Update(ctx, panel.ID, addrInput("painel2.exemplo.com"))
	assert.ErrorIs(t, err, ErrPanelDomain)
	assert.ErrorIs(t, s.Delete(ctx, panel.ID), ErrPanelDomain)
	got, err := s.DB.GetDomain(ctx, panel.ID)
	require.NoError(t, err)
	assert.Equal(t, "painel.exemplo.com", got.Hostname)

	// Inexistente.
	_, _, err = s.Update(ctx, 999, addrInput("x.exemplo.com"))
	assert.ErrorIs(t, err, ErrNotFound)
	assert.ErrorIs(t, s.Delete(ctx, 999), ErrNotFound)
}

func TestCreate_WWWCollision(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t)
	in := addrInput("exemplo.com")
	in.WWWRedirect = "www_to_apex"
	_, _, err := s.Create(ctx, in)
	require.NoError(t, err)

	// www.exemplo.com já é o par www de exemplo.com.
	_, _, err = s.Create(ctx, addrInput("www.exemplo.com"))
	assert.Equal(t, "hostname", invalidField(t, err))

	// Par www do novo colide com um domínio existente.
	_, _, err = s.Create(ctx, addrInput("www.loja.exemplo.com"))
	require.NoError(t, err)
	in = addrInput("loja.exemplo.com")
	in.WWWRedirect = "www_to_apex"
	_, _, err = s.Create(ctx, in)
	assert.Equal(t, "hostname", invalidField(t, err))
}

func TestUpdateDelete(t *testing.T) {
	ctx := context.Background()
	s, fake := newTestService(t)
	d, _, err := s.Create(ctx, addrInput("exemplo.com"))
	require.NoError(t, err)

	in := addrInput("exemplo.com")
	in.Address = "127.0.0.1:6000"
	in.WWWRedirect = "www_to_apex" // o próprio domínio não colide consigo
	u, _, err := s.Update(ctx, d.ID, in)
	require.NoError(t, err)
	assert.Equal(t, d.ID, u.ID)
	assert.Equal(t, "127.0.0.1:6000", u.Address)
	assert.Equal(t, "127.0.0.1:6000", fake.LastApplied()[0].Routes[0].Upstream)

	require.NoError(t, s.Delete(ctx, d.ID))
	_, err = s.DB.GetDomain(ctx, d.ID)
	assert.ErrorIs(t, err, store.ErrNotFound)
	assert.Empty(t, fake.LastApplied())
}

func TestList_CertAndSlug(t *testing.T) {
	ctx := context.Background()
	s, fake := newTestService(t)
	fake.CertFn = func(_ context.Context, host string) proxy.CertInfo {
		if host == "app.exemplo.com" {
			return proxy.CertInfo{State: proxy.CertActive, Issuer: "Let's Encrypt"}
		}
		return proxy.CertInfo{State: proxy.CertPending}
	}
	app := mkApp(t, s.DB, "web", 20001)
	_, _, err := s.Create(ctx, DomainInput{Hostname: "app.exemplo.com", TargetType: TargetApp, AppID: &app.ID})
	require.NoError(t, err)
	_, _, err = s.Create(ctx, addrInput("exemplo.com"))
	require.NoError(t, err)

	list, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "app.exemplo.com", list[0].Hostname)
	assert.Equal(t, "web", list[0].AppSlug)
	assert.Equal(t, proxy.CertActive, list[0].Cert.State)
	assert.Equal(t, "exemplo.com", list[1].Hostname)
	assert.Empty(t, list[1].AppSlug)
	assert.Equal(t, proxy.CertPending, list[1].Cert.State)
}

func TestCheckDNS_DefaultsDoNotPanic(t *testing.T) {
	s := &Service{Resolver: fakeResolver{ips: []net.IP{net.ParseIP("203.0.113.10")}}}
	_ = s.CheckDNS(context.Background(), "exemplo.com")
}
