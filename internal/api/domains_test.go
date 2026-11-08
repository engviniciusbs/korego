package api_test

import (
	"context"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/domains"
	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/node/proxy/proxytest"
	"github.com/engviniciusbs/korego/internal/store"
)

type fakeResolver struct{ ips []net.IP }

func (f fakeResolver) LookupIP(context.Context, string) ([]net.IP, error) { return f.ips, nil }

// domainsEnv monta a API com um domains.Service sobre o proxy falso.
func domainsEnv(t *testing.T, resolved string) (*env, *domains.Service, *proxytest.Fake, *http.Cookie, string) {
	t.Helper()
	fake := &proxytest.Fake{}
	svc := &domains.Service{
		DB: store.OpenMemory(t), Proxy: fake, Hub: events.NewHub(),
		Resolver:  fakeResolver{ips: []net.IP{net.ParseIP(resolved)}},
		HostIPs:   func() ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.10")}, nil },
		PanelPort: 8000, ListenAddr: ":8000",
	}
	e := newEnvDeps(t, api.Deps{Domains: svc})
	_, c, csrf := e.setup(t)
	return e, svc, fake, c, csrf
}

func mut(c *http.Cookie, csrf string) []reqOpt {
	return []reqOpt{withCookie(c), withHeader("X-CSRF-Token", csrf)}
}

func errCode(body map[string]any) string {
	return body["error"].(map[string]any)["code"].(string)
}

func addrDomain(host string) map[string]any {
	return map[string]any{"hostname": host, "targetType": "address", "address": "127.0.0.1:5000", "forceHttps": true}
}

func TestDomainsAPI_CreateReturnsDNSWarning(t *testing.T) {
	e, _, fake, c, csrf := domainsEnv(t, "198.51.100.7") // IP errado
	w := e.do(t, http.MethodPost, "/api/domains", addrDomain("app.exemplo.com"), mut(c, csrf)...)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	b := decode(t, w)
	d := b["domain"].(map[string]any)
	assert.Equal(t, "app.exemplo.com", d["hostname"])
	assert.Equal(t, "address", d["targetType"])
	assert.Equal(t, true, d["forceHttps"])
	assert.Equal(t, false, d["isPanel"])
	assert.NotZero(t, d["id"])
	assert.Contains(t, d, "createdAt")
	assert.Contains(t, d, "cert")
	dns := b["dns"].(map[string]any)
	assert.Equal(t, false, dns["ok"])
	assert.NotEmpty(t, dns["message"])
	assert.Contains(t, dns, "expected")
	assert.Equal(t, 1, fake.Applies())

	w = e.do(t, http.MethodGet, "/api/domains", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, decode(t, w)["domains"].([]any), 1)

	w = e.do(t, http.MethodGet, "/api/domains/1/dns", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, decode(t, w), "ok")

	in := addrDomain("app.exemplo.com")
	in["address"] = "127.0.0.1:5001"
	w = e.do(t, http.MethodPut, "/api/domains/1", in, mut(c, csrf)...)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "127.0.0.1:5001", decode(t, w)["domain"].(map[string]any)["address"])

	w = e.do(t, http.MethodDelete, "/api/domains/1", nil, mut(c, csrf)...)
	assert.Equal(t, http.StatusNoContent, w.Code)
	w = e.do(t, http.MethodDelete, "/api/domains/1", nil, mut(c, csrf)...)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "DOMAIN_NOT_FOUND", errCode(decode(t, w)))
}

func TestDomainsAPI_InvalidAndConflict(t *testing.T) {
	e, _, _, c, csrf := domainsEnv(t, "203.0.113.10")
	w := e.do(t, http.MethodPost, "/api/domains", addrDomain("Inválido!"), mut(c, csrf)...)
	require.Equal(t, http.StatusBadRequest, w.Code)
	er := decode(t, w)["error"].(map[string]any)
	assert.Equal(t, "INVALID_DOMAIN", er["code"])
	assert.Equal(t, "hostname", er["field"])
	assert.NotEmpty(t, er["message"])

	w = e.do(t, http.MethodPost, "/api/domains", addrDomain("app.exemplo.com"), mut(c, csrf)...)
	require.Equal(t, http.StatusCreated, w.Code)
	w = e.do(t, http.MethodPost, "/api/domains", addrDomain("app.exemplo.com"), mut(c, csrf)...)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Equal(t, "DOMAIN_EXISTS", errCode(decode(t, w)))

	// Corpo desconhecido e ids ruins.
	w = e.do(t, http.MethodPost, "/api/domains", map[string]any{"x": 1}, mut(c, csrf)...)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	for _, id := range []string{"abc", "0", "-1", "1.5"} {
		for _, req := range [][2]string{{http.MethodPut, ""}, {http.MethodDelete, ""}, {http.MethodGet, "/dns"}} {
			w = e.do(t, req[0], "/api/domains/"+id+req[1], addrDomain("x.exemplo.com"), mut(c, csrf)...)
			require.Equal(t, http.StatusBadRequest, w.Code, req[0]+id+req[1])
			assert.Equal(t, "INVALID_DOMAIN_ID", errCode(decode(t, w)))
		}
	}
	w = e.do(t, http.MethodGet, "/api/domains/99/dns", nil, withCookie(c))
	assert.Equal(t, http.StatusNotFound, w.Code)

	// Sem sessão e sem CSRF.
	assert.Equal(t, http.StatusUnauthorized, e.do(t, http.MethodGet, "/api/domains", nil).Code)
	assert.Equal(t, http.StatusForbidden, e.do(t, http.MethodPost, "/api/domains", addrDomain("o.exemplo.com"), withCookie(c)).Code)
}

func TestDomainsAPI_PanelProtected(t *testing.T) {
	e, svc, _, c, csrf := domainsEnv(t, "203.0.113.10")
	require.NoError(t, svc.SetPanelDomain(context.Background(), "painel.exemplo.com"))

	w := e.do(t, http.MethodPut, "/api/domains/1", addrDomain("outro.exemplo.com"), mut(c, csrf)...)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Equal(t, "PANEL_DOMAIN_PROTECTED", errCode(decode(t, w)))
	w = e.do(t, http.MethodDelete, "/api/domains/1", nil, mut(c, csrf)...)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "PANEL_DOMAIN_PROTECTED", errCode(decode(t, w)))
}

func TestDomainsAPI_Audited(t *testing.T) {
	e, _, _, c, csrf := domainsEnv(t, "203.0.113.10")
	e.do(t, http.MethodPost, "/api/domains", addrDomain("app.exemplo.com"), mut(c, csrf)...)
	e.do(t, http.MethodPost, "/api/domains", addrDomain("app.exemplo.com"), mut(c, csrf)...) // conflito
	e.do(t, http.MethodPut, "/api/domains/1", addrDomain("app.exemplo.com"), mut(c, csrf)...)
	e.do(t, http.MethodDelete, "/api/domains/1", nil, mut(c, csrf)...)

	w := e.do(t, http.MethodGet, "/api/audit", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code)
	type key struct {
		action, target string
		ok             bool
	}
	got := map[key]bool{}
	for _, it := range decode(t, w)["items"].([]any) {
		m := it.(map[string]any)
		md, _ := m["metadata"].(map[string]any)
		ok, _ := md["ok"].(bool)
		got[key{m["action"].(string), m["target"].(string), ok}] = true
	}
	assert.True(t, got[key{"domain.create", "app.exemplo.com", true}], got)
	assert.True(t, got[key{"domain.create", "app.exemplo.com", false}], got)
	assert.True(t, got[key{"domain.update", "app.exemplo.com", true}], got)
	assert.True(t, got[key{"domain.delete", "app.exemplo.com", true}], got)
}

func TestProxyAPI_StatusAndRetry(t *testing.T) {
	e, _, fake, c, csrf := domainsEnv(t, "203.0.113.10")
	assert.Equal(t, http.StatusUnauthorized, e.do(t, http.MethodGet, "/api/proxy", nil).Code)

	fake.EnsureFn = func(context.Context) proxy.Status {
		return proxy.Status{State: proxy.StateBlocked, Process: "nginx (pid 1)", Message: "porta 80 ocupada"}
	}
	w := e.do(t, http.MethodPost, "/api/proxy/retry", nil, mut(c, csrf)...)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	b := decode(t, w)
	assert.Equal(t, "blocked", b["state"])
	assert.Equal(t, "nginx (pid 1)", b["process"])
	assert.Contains(t, b["guideUrl"], "migracao-nginx.md")

	w = e.do(t, http.MethodGet, "/api/proxy", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code)
	b = decode(t, w)
	assert.Equal(t, "blocked", b["state"])
	assert.Contains(t, b["guideUrl"], "migracao-nginx.md")

	assert.Equal(t, http.StatusForbidden, e.do(t, http.MethodPost, "/api/proxy/retry", nil, withCookie(c)).Code)
}
