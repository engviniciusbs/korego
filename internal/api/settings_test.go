package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/proxy"
)

func withHost(h string) reqOpt { return func(r *http.Request) { r.Host = h } }

func TestPanelAPI_DisablePortOverPlainHTTP409(t *testing.T) {
	e, svc, _, c, csrf := domainsEnv(t, "203.0.113.10")
	require.NoError(t, svc.SetPanelDomain(context.Background(), "painel.exemplo.com"))
	w := e.do(t, http.MethodPut, "/api/settings/panel/public-port", map[string]any{"enabled": false}, mut(c, csrf)...)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	er := decode(t, w)["error"].(map[string]any)
	assert.Equal(t, "PANEL_NOT_HTTPS", er["code"])
	assert.Equal(t, "Acesse o painel pelo domínio com HTTPS antes de desativar a porta 8000.", er["message"])

	w = e.do(t, http.MethodGet, "/api/settings/panel", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code)
	b := decode(t, w)
	assert.Equal(t, true, b["publicPort"])
	assert.Equal(t, "painel.exemplo.com", b["domain"])
	assert.Equal(t, false, b["requestHTTPS"])
	assert.Equal(t, "example.com", b["requestHost"])
}

func TestPanelAPI_DisablePortViaPanelDomain(t *testing.T) {
	e, svc, fake, c, csrf := domainsEnv(t, "203.0.113.10")
	var addrs []string
	svc.OnListenChange = func(a string) error { addrs = append(addrs, a); return nil }
	fake.CertFn = func(context.Context, string) proxy.CertInfo { return proxy.CertInfo{State: proxy.CertActive} }
	require.NoError(t, svc.SetPanelDomain(context.Background(), "painel.exemplo.com"))

	opts := append(mut(c, csrf), withRemote("127.0.0.1:4444"), withHeader("X-Forwarded-Proto", "https"))
	// Host de outro domínio: recusa.
	w := e.do(t, http.MethodPut, "/api/settings/panel/public-port", map[string]any{"enabled": false},
		append(opts, withHost("outro.exemplo.com"))...)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())

	w = e.do(t, http.MethodPut, "/api/settings/panel/public-port", map[string]any{"enabled": false},
		append(opts, withHost("Painel.Exemplo.com:443"))...)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, []string{"127.0.0.1:8000"}, addrs)
	b := decode(t, w)
	assert.Equal(t, false, b["publicPort"])
	assert.Equal(t, "127.0.0.1:8000", b["listenAddr"])
	assert.Equal(t, true, b["requestHTTPS"])

	// Com a porta desligada, remover o domínio é recusado.
	w = e.do(t, http.MethodPut, "/api/settings/panel/domain", map[string]any{"hostname": ""}, mut(c, csrf)...)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "PANEL_PORT_OFF", errCode(decode(t, w)))

	// Auditoria: uma recusa e um sucesso.
	w = e.do(t, http.MethodGet, "/api/audit", nil, withCookie(c))
	seen := 0
	for _, it := range decode(t, w)["items"].([]any) {
		if it.(map[string]any)["action"] == "panel.public_port" {
			seen++
		}
	}
	assert.Equal(t, 2, seen)
}

// X-Forwarded-Proto e Host forjados por um cliente remoto (fora do loopback,
// ou seja, sem passar pelo Caddy) não contam como acesso por HTTPS.
func TestPanelAPI_RemoteForgedForwardedProtoRejected(t *testing.T) {
	e, svc, fake, c, csrf := domainsEnv(t, "203.0.113.10")
	called := false
	svc.OnListenChange = func(string) error { called = true; return nil }
	fake.CertFn = func(context.Context, string) proxy.CertInfo { return proxy.CertInfo{State: proxy.CertActive} }
	require.NoError(t, svc.SetPanelDomain(context.Background(), "painel.exemplo.com"))

	opts := append(mut(c, csrf), withRemote("203.0.113.5:1234"), withHeader("X-Forwarded-Proto", "https"),
		withHost("painel.exemplo.com"))
	w := e.do(t, http.MethodPut, "/api/settings/panel/public-port", map[string]any{"enabled": false}, opts...)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Equal(t, "PANEL_NOT_HTTPS", errCode(decode(t, w)))
	assert.False(t, called, "OnListenChange não pode ser chamado")
}

func TestPanelAPI_SetDomain(t *testing.T) {
	e, svc, fake, c, csrf := domainsEnv(t, "203.0.113.10")
	w := e.do(t, http.MethodPut, "/api/settings/panel/domain", map[string]any{"hostname": "Inválido!"}, mut(c, csrf)...)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "hostname", decode(t, w)["error"].(map[string]any)["field"])

	w = e.do(t, http.MethodPut, "/api/settings/panel/domain", map[string]any{"hostname": "painel.exemplo.com"}, mut(c, csrf)...)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "painel.exemplo.com", decode(t, w)["domain"])

	// Falha do proxy depois de gravar: 200 com saved/proxyError.
	fake.ApplyFn = func(context.Context, []proxy.Site, proxy.Listen, []string) error { return assert.AnError }
	w = e.do(t, http.MethodPut, "/api/settings/panel/domain", map[string]any{"hostname": "novo.exemplo.com"}, mut(c, csrf)...)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	b := decode(t, w)
	assert.Equal(t, true, b["saved"])
	assert.NotEmpty(t, b["proxyError"])
	p, err := svc.Panel(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "novo.exemplo.com", p.Domain)
}
