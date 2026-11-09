package domains

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSites_AppAddressPanel(t *testing.T) {
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

	routes, err := s.Sites(ctx)
	require.NoError(t, err)
	assert.Equal(t, []proxy.Site{
		{Host: "app.exemplo.com", ForceHTTPS: true, WWWRedirect: proxy.WWWNone, TLS: autoTLS, Routes: []proxy.SiteRoute{{Path: "/", Upstream: "127.0.0.1:20001"}}},
		{Host: "exemplo.com", WWWRedirect: proxy.WWWToApex, TLS: autoTLS, Routes: []proxy.SiteRoute{{Path: "/", Upstream: "127.0.0.1:5000"}}},
		{Host: "painel.exemplo.com", ForceHTTPS: true, WWWRedirect: proxy.WWWNone, TLS: autoTLS, Routes: []proxy.SiteRoute{{Path: "/", Upstream: "127.0.0.1:8000"}}},
	}, routes)

	_, err = proxy.BuildConfig(routes, proxy.ListenNormal, nil)
	assert.NoError(t, err)
}

func TestSites_SkipsConflictingRows(t *testing.T) {
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

	routes, err := s.Sites(ctx)
	require.NoError(t, err)
	assert.Equal(t, []proxy.Site{
		{Host: "exemplo.com", WWWRedirect: proxy.WWWToApex, TLS: autoTLS, Routes: []proxy.SiteRoute{{Path: "/", Upstream: "127.0.0.1:5000"}}},
		{Host: "www.zz.exemplo.com", WWWRedirect: proxy.WWWNone, TLS: autoTLS, Routes: []proxy.SiteRoute{{Path: "/", Upstream: "127.0.0.1:8000"}}},
	}, routes)
	_, err = proxy.BuildConfig(routes, proxy.ListenNormal, nil)
	assert.NoError(t, err)
}

func TestSites_SkipsInvalidWWWMode(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t)
	ins := func(d store.Domain) {
		require.NoError(t, s.DB.InsertDomain(ctx, &d))
	}
	ins(store.Domain{Hostname: "www.exemplo.com", TargetType: TargetAddress, Address: "127.0.0.1:5000", WWWRedirect: "www_to_apex"})
	ins(store.Domain{Hostname: "loja.exemplo.com", TargetType: TargetAddress, Address: "127.0.0.1:5001", WWWRedirect: "apex_to_www"})
	ins(store.Domain{Hostname: "x.exemplo.com", TargetType: TargetAddress, Address: "127.0.0.1:5002", WWWRedirect: "qualquer"})
	ins(store.Domain{Hostname: "ok.exemplo.com", TargetType: TargetAddress, Address: "127.0.0.1:5003", WWWRedirect: "none"})

	routes, err := s.Sites(ctx)
	require.NoError(t, err)
	assert.Equal(t, []proxy.Site{{Host: "ok.exemplo.com", WWWRedirect: proxy.WWWNone, TLS: autoTLS, Routes: []proxy.SiteRoute{{Path: "/", Upstream: "127.0.0.1:5003"}}}}, routes)
	_, err = proxy.BuildConfig(routes, proxy.ListenNormal, nil)
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

func TestSites_OldStyleInputIsSingleRoute(t *testing.T) {
	ctx := context.Background()
	s, fake := newTestService(t)
	d, _, err := s.Create(ctx, addrInput("exemplo.com"))
	require.NoError(t, err)
	require.Len(t, d.Routes, 1)
	assert.Equal(t, "/", d.Routes[0].Path)
	assert.Equal(t, TargetAddress, d.Routes[0].TargetType)
	assert.Equal(t, "127.0.0.1:5000", d.Routes[0].Address)
	assert.True(t, d.Compression, "compressão ligada por padrão na criação")
	assert.Equal(t, proxy.TLSAuto, d.TLSMode)

	sites, err := s.Sites(ctx)
	require.NoError(t, err)
	want := []proxy.Site{{Host: "exemplo.com", ForceHTTPS: true, WWWRedirect: proxy.WWWNone, TLS: autoTLS,
		Compression: true, Routes: []proxy.SiteRoute{{Path: "/", Upstream: "127.0.0.1:5000"}}}}
	assert.Equal(t, want, sites)
	assert.Equal(t, want, fake.LastApplied())

	// Update com corpo antigo mantém a compressão gravada (nil = mantém).
	off := false
	in := addrInput("exemplo.com")
	in.Compression = &off
	_, _, err = s.Update(ctx, d.ID, in)
	require.NoError(t, err)
	u, _, err := s.Update(ctx, d.ID, addrInput("exemplo.com"))
	require.NoError(t, err)
	assert.False(t, u.Compression)
}

func TestSites_RoutesStaticHeadersAndOptions(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t)
	s.Ranges = fixedRanges{"203.0.113.0/24", "2001:db8::/32"}
	app := mkApp(t, s.DB, "web", 20001)
	semPorta := mkApp(t, s.DB, "nova", 0)
	off := false
	_, _, err := s.Create(ctx, DomainInput{Hostname: "exemplo.com", WWWRedirect: WWWNone, ForceHTTPS: ptr(true),
		Routes: []RouteInput{
			{Path: "/", TargetType: TargetApp, AppID: &app.ID, MaxBodyBytes: 1024, NoBuffering: true},
			{Path: "/static/", TargetType: TargetStatic, Root: "/var/www/site/", SPA: true, ImmutablePrefix: "/assets/"},
			{Path: "/api/", TargetType: TargetAddress, Address: "127.0.0.1:5000"},
			{Path: "/nova/", TargetType: TargetApp, AppID: &semPorta.ID}, // sem porta: fica de fora
		},
		CloudflareOnly: ptr(true), Compression: &off,
		Headers: []store.Header{{Name: "X-Frame-Options", Value: "DENY"}}})
	require.NoError(t, err)

	sites, err := s.Sites(ctx)
	require.NoError(t, err)
	assert.Equal(t, []proxy.Site{{Host: "exemplo.com", ForceHTTPS: true, WWWRedirect: proxy.WWWNone, TLS: autoTLS,
		CloudflareOnly: true, Headers: []proxy.Header{{Name: "X-Frame-Options", Value: "DENY"}},
		Routes: []proxy.SiteRoute{
			{Path: "/", Upstream: "127.0.0.1:20001", MaxBodyBytes: 1024, NoBuffering: true},
			{Path: "/static/", Static: &proxy.StaticTarget{Root: "/var/www/site", SPA: true, ImmutablePrefix: "/assets/"}},
			{Path: "/api/", Upstream: "127.0.0.1:5000"},
		}}}, sites)

	// Sem faixas da Cloudflare o site só Cloudflare não pode ser montado:
	// fica de fora sem derrubar os demais.
	_, _, err = s.Create(ctx, addrInput("loja.exemplo.net"))
	require.NoError(t, err)
	s.Ranges = nil
	sites, err = s.Sites(ctx)
	require.NoError(t, err)
	require.Len(t, sites, 1)
	assert.Equal(t, "loja.exemplo.net", sites[0].Host)
}

func TestSites_CustomCertIncludedAndBadCertSkipped(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t)
	cs := withCerts(t, s)
	c := addCert(t, cs, "exemplo.com", "www.exemplo.com")
	in := addrInput("exemplo.com")
	in.WWWRedirect = WWWToApex
	in.TLS = &TLSInput{Mode: proxy.TLSCustom, CertID: &c.ID}
	_, _, err := s.Create(ctx, in)
	require.NoError(t, err)
	bad := addCert(t, cs, "loja.exemplo.net")
	in = addrInput("loja.exemplo.net")
	in.TLS = &TLSInput{Mode: proxy.TLSCustom, CertID: &bad.ID}
	_, _, err = s.Create(ctx, in)
	require.NoError(t, err)
	_, _, err = s.Create(ctx, addrInput("app.exemplo.com"))
	require.NoError(t, err)

	pemText, err := cs.PEM(ctx, c.ID)
	require.NoError(t, err)

	// O PEM de "bad" não decifra mais (chave trocada): só esse domínio sai.
	_, err = s.DB.SQL().ExecContext(ctx, `UPDATE certificates SET pem_enc = 'lixo' WHERE id = ?`, bad.ID)
	require.NoError(t, err)

	sites, err := s.Sites(ctx)
	require.NoError(t, err)
	require.Len(t, sites, 2)
	assert.Equal(t, "app.exemplo.com", sites[0].Host)
	assert.Equal(t, autoTLS, sites[0].TLS)
	assert.Equal(t, "exemplo.com", sites[1].Host)
	assert.Equal(t, proxy.SiteTLS{Mode: proxy.TLSCustom, CertPEM: pemText}, sites[1].TLS)
	_, err = proxy.BuildConfig(sites, proxy.ListenNormal, nil)
	assert.NoError(t, err)

	// Sem o serviço de certificados, o domínio custom também sai.
	s.Certs = nil
	sites, err = s.Sites(ctx)
	require.NoError(t, err)
	require.Len(t, sites, 1)
	assert.Equal(t, "app.exemplo.com", sites[0].Host)
}

func TestSites_BadStoredRowSkipped(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t)
	ins := func(d store.Domain) {
		require.NoError(t, s.DB.InsertDomain(ctx, &d))
	}
	// Linhas gravadas direto no banco que o BuildConfig recusaria.
	ins(store.Domain{Hostname: "a.exemplo.com", WWWRedirect: "none", Headers: []store.Header{{Name: "X-A", Value: "1"}, {Name: "x-a", Value: "2"}},
		Routes: []store.DomainRoute{{Path: "/", TargetType: TargetAddress, Address: "127.0.0.1:5000"}}})
	ins(store.Domain{Hostname: "b.exemplo.com", WWWRedirect: "none",
		Routes: []store.DomainRoute{{Path: "/", TargetType: TargetAddress, Address: "{env.X}:80"}}})
	ins(store.Domain{Hostname: "c.exemplo.com", WWWRedirect: "none",
		Routes: []store.DomainRoute{{Path: "/", TargetType: TargetStatic, Root: "/etc"}}})
	ins(store.Domain{Hostname: "d.exemplo.com", WWWRedirect: "none",
		Routes: []store.DomainRoute{{Path: "/api/", TargetType: TargetAddress, Address: "127.0.0.1:5000"}}})
	ins(store.Domain{Hostname: "ok.exemplo.com", WWWRedirect: "none",
		Routes: []store.DomainRoute{{Path: "/", TargetType: TargetAddress, Address: "127.0.0.1:5001"}}})
	sites, err := s.Sites(ctx)
	require.NoError(t, err)
	require.Len(t, sites, 1)
	assert.Equal(t, "ok.exemplo.com", sites[0].Host)
}

func TestSites_PanelUnreadableCertFallsBackToAuto(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t)
	cs := withCerts(t, s)
	require.NoError(t, s.SetPanelDomain(ctx, "painel.exemplo.com"))
	panel, _, err := s.DB.PanelDomain(ctx)
	require.NoError(t, err)
	c := addCert(t, cs, "painel.exemplo.com")
	panel.TLSMode, panel.CertID = proxy.TLSCustom, &c.ID
	require.NoError(t, s.DB.UpdateDomain(ctx, &panel))
	other := addCert(t, cs, "loja.exemplo.net")
	d := store.Domain{Hostname: "loja.exemplo.net", WWWRedirect: "none", TLSMode: proxy.TLSCustom, CertID: &other.ID,
		Routes: []store.DomainRoute{{Path: "/", TargetType: TargetAddress, Address: "127.0.0.1:5000"}}}
	require.NoError(t, s.DB.InsertDomain(ctx, &d))

	sites, err := s.Sites(ctx)
	require.NoError(t, err)
	require.Len(t, sites, 2)
	assert.Equal(t, proxy.TLSCustom, sites[1].TLS.Mode)

	// PEM que não decifra: o painel continua, com certificado automático; o
	// outro domínio sai.
	_, err = s.DB.SQL().ExecContext(ctx, `UPDATE certificates SET pem_enc = 'lixo'`)
	require.NoError(t, err)
	sites, err = s.Sites(ctx)
	require.NoError(t, err)
	require.Len(t, sites, 1)
	assert.Equal(t, "painel.exemplo.com", sites[0].Host)
	assert.Equal(t, autoTLS, sites[0].TLS)

	// PEM que decifra mas não serve (chave de outro certificado): idem.
	var bad []byte
	certA := genCertPEM(t, time.Now().Add(time.Hour), "painel.exemplo.com")
	certB := genCertPEM(t, time.Now().Add(time.Hour), "painel.exemplo.com")
	bad = append(bad, certA[:bytes.Index(certA, []byte("-----BEGIN EC"))]...)
	bad = append(bad, certB[bytes.Index(certB, []byte("-----BEGIN EC")):]...)
	enc, err := cs.Box.Encrypt(bad)
	require.NoError(t, err)
	_, err = s.DB.SQL().ExecContext(ctx, `UPDATE certificates SET pem_enc = ? WHERE id = ?`, enc, c.ID)
	require.NoError(t, err)
	sites, err = s.Sites(ctx)
	require.NoError(t, err)
	require.Len(t, sites, 1)
	assert.Equal(t, autoTLS, sites[0].TLS)

	// Regravar o mesmo host não confere o certificado de novo.
	require.NoError(t, s.SetPanelDomain(ctx, "painel.exemplo.com"))
}
