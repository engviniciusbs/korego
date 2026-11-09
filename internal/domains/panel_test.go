package domains

import (
	"context"
	"errors"
	"testing"

	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/node/proxy/proxytest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPanelService(t *testing.T) (*Service, *[]string) {
	t.Helper()
	s, fake := newTestService(t)
	s.ListenAddr = ":8000"
	fake.CertFn = func(context.Context, string) proxy.CertInfo {
		return proxy.CertInfo{State: proxy.CertActive}
	}
	var calls []string
	s.OnListenChange = func(addr string) error {
		calls = append(calls, addr)
		return nil
	}
	return s, &calls
}

func TestPanel_DisableRequiresHTTPS(t *testing.T) {
	ctx := context.Background()
	s, calls := newPanelService(t)
	fake := s.Proxy.(*proxytest.Fake)

	// Sem domínio do painel não dá para desligar.
	assert.ErrorIs(t, s.SetPublicPort(ctx, false, true), ErrPanelNotHTTPS)

	require.NoError(t, s.SetPanelDomain(ctx, "painel.exemplo.com"))

	// Fora do domínio do painel com HTTPS.
	assert.ErrorIs(t, s.SetPublicPort(ctx, false, false), ErrPanelNotHTTPS)

	// Certificado pendente.
	fake.CertFn = func(context.Context, string) proxy.CertInfo {
		return proxy.CertInfo{State: proxy.CertPending}
	}
	assert.ErrorIs(t, s.SetPublicPort(ctx, false, true), ErrPanelNotHTTPS)
	assert.Empty(t, *calls)

	p, err := s.Panel(ctx)
	require.NoError(t, err)
	assert.True(t, p.PublicPort)
	assert.Equal(t, ":8000", p.ListenAddr)

	// Certificado ativo e acesso pelo domínio com HTTPS.
	fake.CertFn = func(context.Context, string) proxy.CertInfo {
		return proxy.CertInfo{State: proxy.CertActive}
	}
	require.NoError(t, s.SetPublicPort(ctx, false, true))
	assert.Equal(t, []string{"127.0.0.1:8000"}, *calls)
	v, ok, err := s.DB.GetSetting(ctx, "panel.public_port")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "false", v)

	p, err = s.Panel(ctx)
	require.NoError(t, err)
	assert.False(t, p.PublicPort)
	assert.Equal(t, "127.0.0.1:8000", p.ListenAddr)
	assert.Equal(t, "painel.exemplo.com", p.Domain)
	assert.NotZero(t, p.DomainID)
	assert.Equal(t, proxy.CertActive, p.Cert.State)

	// Religar é sempre permitido, mesmo sem HTTPS.
	require.NoError(t, s.SetPublicPort(ctx, true, false))
	assert.Equal(t, []string{"127.0.0.1:8000", ":8000"}, *calls)
	v, _, err = s.DB.GetSetting(ctx, "panel.public_port")
	require.NoError(t, err)
	assert.Equal(t, "true", v)
}

func TestPanel_ListenChangeFailureRestoresSetting(t *testing.T) {
	ctx := context.Background()
	s, _ := newPanelService(t)
	require.NoError(t, s.SetPanelDomain(ctx, "painel.exemplo.com"))
	boom := errors.New("porta ocupada")
	s.OnListenChange = func(string) error { return boom }

	assert.ErrorIs(t, s.SetPublicPort(ctx, false, true), boom)
	_, ok, err := s.DB.GetSetting(ctx, "panel.public_port")
	require.NoError(t, err)
	assert.False(t, ok, "configuração ausente volta a ficar ausente")

	require.NoError(t, s.DB.SetSetting(ctx, "panel.public_port", "false"))
	assert.ErrorIs(t, s.SetPublicPort(ctx, true, false), boom)
	v, _, err := s.DB.GetSetting(ctx, "panel.public_port")
	require.NoError(t, err)
	assert.Equal(t, "false", v)
}

func TestPanel_SetDomainCreatesPanelRoute(t *testing.T) {
	ctx := context.Background()
	s, _ := newPanelService(t)
	fake := s.Proxy.(*proxytest.Fake)

	p, err := s.Panel(ctx)
	require.NoError(t, err)
	assert.Empty(t, p.Domain)
	assert.Zero(t, p.DomainID)

	require.NoError(t, s.SetPanelDomain(ctx, "painel.exemplo.com"))
	want := []proxy.Site{{Host: "painel.exemplo.com",
		ForceHTTPS: true, WWWRedirect: proxy.WWWNone, Routes: []proxy.SiteRoute{{Path: "/", Upstream: "127.0.0.1:8000"}}}}
	routes, err := s.Routes(ctx)
	require.NoError(t, err)
	assert.Equal(t, want, routes)
	assert.Equal(t, want, fake.LastApplied())

	d, ok, err := s.DB.PanelDomain(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, TargetAddress, d.TargetType)
	assert.Equal(t, "127.0.0.1:8000", d.Address)
	assert.Equal(t, WWWNone, d.WWWRedirect)
	firstID := d.ID

	// Trocar o host substitui a linha (continua uma só).
	require.NoError(t, s.SetPanelDomain(ctx, "admin.exemplo.com"))
	routes, err = s.Routes(ctx)
	require.NoError(t, err)
	assert.Equal(t, []proxy.Site{{Host: "admin.exemplo.com",
		ForceHTTPS: true, WWWRedirect: proxy.WWWNone, Routes: []proxy.SiteRoute{{Path: "/", Upstream: "127.0.0.1:8000"}}}}, routes)
	list, err := s.DB.ListDomains(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, firstID, list[0].ID)

	// Inválido e colisão.
	var ie *ErrInvalid
	require.ErrorAs(t, s.SetPanelDomain(ctx, "Painel"), &ie)
	assert.Equal(t, "hostname", ie.Field)
	_, _, err = s.Create(ctx, DomainInput{Hostname: "exemplo.com", TargetType: TargetAddress,
		Address: "127.0.0.1:5000", WWWRedirect: WWWToApex})
	require.NoError(t, err)
	require.ErrorAs(t, s.SetPanelDomain(ctx, "www.exemplo.com"), &ie)

	// Proxy inativo não impede a gravação.
	fake.ApplyFn = func(context.Context, []proxy.Site, proxy.Listen, []string) error { return proxy.ErrInactive }
	require.NoError(t, s.SetPanelDomain(ctx, "painel.exemplo.com"))

	// Vazio remove.
	fake.ApplyFn = nil
	require.NoError(t, s.SetPanelDomain(ctx, ""))
	_, ok, err = s.DB.PanelDomain(ctx)
	require.NoError(t, err)
	assert.False(t, ok)
	routes, err = s.Routes(ctx)
	require.NoError(t, err)
	for _, r := range routes {
		assert.NotEqual(t, "127.0.0.1:8000", r.Routes[0].Upstream)
	}
	// Remover sem domínio não é erro.
	require.NoError(t, s.SetPanelDomain(ctx, ""))
}

func TestEffectiveListenAddr(t *testing.T) {
	for _, c := range []struct {
		in     string
		public bool
		want   string
	}{
		{":8000", true, ":8000"},
		{":8000", false, "127.0.0.1:8000"},
		{"0.0.0.0:9000", false, "127.0.0.1:9000"},
		{"203.0.113.10:8000", true, "203.0.113.10:8000"},
	} {
		got, err := EffectiveListenAddr(c.in, c.public)
		require.NoError(t, err)
		assert.Equal(t, c.want, got, c.in)
	}
	_, err := EffectiveListenAddr("sem-porta", false)
	assert.Error(t, err)
}

func TestPublicPortSetting(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t)
	on, err := PublicPort(ctx, s.DB)
	require.NoError(t, err)
	assert.True(t, on, "ausente = ligada")
	require.NoError(t, s.DB.SetSetting(ctx, "panel.public_port", "false"))
	on, err = PublicPort(ctx, s.DB)
	require.NoError(t, err)
	assert.False(t, on)
}

func TestPanel_SetDomainBlockedWithPublicPortOff(t *testing.T) {
	ctx := context.Background()
	s, _ := newPanelService(t)

	// Porta ligada: trocar é permitido.
	require.NoError(t, s.SetPanelDomain(ctx, "painel.exemplo.com"))
	require.NoError(t, s.SetPanelDomain(ctx, "admin.exemplo.com"))

	require.NoError(t, s.SetPublicPort(ctx, false, true))

	// Remover → recusado e a linha continua.
	assert.ErrorIs(t, s.SetPanelDomain(ctx, ""), ErrPanelPortOff)
	d, ok, err := s.DB.PanelDomain(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "admin.exemplo.com", d.Hostname)

	// Outro host → recusado.
	assert.ErrorIs(t, s.SetPanelDomain(ctx, "painel.exemplo.com"), ErrPanelPortOff)
	d, _, err = s.DB.PanelDomain(ctx)
	require.NoError(t, err)
	assert.Equal(t, "admin.exemplo.com", d.Hostname)

	// Mesmo host → permitido.
	require.NoError(t, s.SetPanelDomain(ctx, " admin.exemplo.com "))

	// Porta religada: trocar volta a ser permitido.
	require.NoError(t, s.SetPublicPort(ctx, true, false))
	require.NoError(t, s.SetPanelDomain(ctx, "painel.exemplo.com"))
	require.NoError(t, s.SetPanelDomain(ctx, ""))
}
