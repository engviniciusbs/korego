package api_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/certs"
	"github.com/engviniciusbs/korego/internal/domains"
	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/node/proxy/proxytest"
	"github.com/engviniciusbs/korego/internal/secrets"
	"github.com/engviniciusbs/korego/internal/store"
)

type fakeResolver struct{ ips []net.IP }

type fixedRanges []string

func (f fixedRanges) Get() []string { return f }

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
		Ranges: fixedRanges{"203.0.113.0/24"},
	}
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	box, err := secrets.NewBox(key)
	require.NoError(t, err)
	svc.Certs = &certs.Service{DB: svc.DB, Box: box}
	e := newEnvDeps(t, api.Deps{Domains: svc, Certs: svc.Certs})
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
	er := decode(t, w)["error"].(map[string]any)
	assert.Equal(t, "DOMAIN_NOT_FOUND", er["code"])
	assert.NotContains(t, er, "field", "field só aparece em erros de validação")
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

// auditMeta devolve a metadata da última entrada de auditoria com action e target.
func auditMeta(t *testing.T, e *env, c *http.Cookie, action, target string) map[string]any {
	t.Helper()
	w := e.do(t, http.MethodGet, "/api/audit", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code)
	for _, it := range decode(t, w)["items"].([]any) {
		m := it.(map[string]any)
		if m["action"] == action && m["target"] == target {
			md, _ := m["metadata"].(map[string]any)
			return md
		}
	}
	t.Fatalf("auditoria %s %s não encontrada", action, target)
	return nil
}

func TestAPI_DomainCreateWithRoutesAndAudit(t *testing.T) {
	e, svc, fake, c, csrf := domainsEnv(t, "203.0.113.10")
	cert, err := svc.Certs.Add(context.Background(), "loja", []byte(genCertPEM(t, "loja.exemplo.net", time.Now().Add(20*24*time.Hour))))
	require.NoError(t, err)
	body := map[string]any{
		"hostname": "loja.exemplo.net", "wwwRedirect": "none", "forceHttps": true,
		"routes": []any{
			map[string]any{"path": "/", "targetType": "address", "address": "127.0.0.1:5000", "maxBodyBytes": 1048576, "noBuffering": true},
			map[string]any{"path": "/static/", "targetType": "static", "root": "/var/www/loja", "spa": true, "immutablePrefix": "/assets/"},
		},
		"tls":            map[string]any{"mode": "custom", "certId": cert.ID},
		"cloudflareOnly": true,
		"headers":        []any{map[string]any{"name": "X-Frame-Options", "value": "DENY"}},
		"compression":    false,
	}
	w := e.do(t, http.MethodPost, "/api/domains", body, mut(c, csrf)...)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), "PRIVATE KEY")
	d := decode(t, w)["domain"].(map[string]any)
	// Campos antigos derivados da rota "/".
	assert.Equal(t, "address", d["targetType"])
	assert.Equal(t, "127.0.0.1:5000", d["address"])
	assert.Equal(t, true, d["cloudflareOnly"])
	assert.Equal(t, false, d["compression"])
	assert.Equal(t, []any{map[string]any{"name": "X-Frame-Options", "value": "DENY"}}, d["headers"])
	routes := d["routes"].([]any)
	require.Len(t, routes, 2)
	assert.Equal(t, map[string]any{"path": "/", "targetType": "address", "address": "127.0.0.1:5000",
		"maxBodyBytes": float64(1048576), "noBuffering": true, "spa": false}, routes[0])
	assert.Equal(t, map[string]any{"path": "/static/", "targetType": "static", "root": "/var/www/loja",
		"maxBodyBytes": float64(0), "noBuffering": false, "spa": true, "immutablePrefix": "/assets/"}, routes[1])
	tls := d["tls"].(map[string]any)
	assert.Equal(t, "custom", tls["mode"])
	assert.Equal(t, float64(cert.ID), tls["certId"])
	assert.Equal(t, "loja", tls["certName"])
	assert.NotEmpty(t, tls["notAfter"])
	assert.Equal(t, true, tls["expiresSoon"])

	site := fake.LastApplied()[0]
	assert.Equal(t, "custom", site.TLS.Mode)
	assert.Equal(t, []string{"203.0.113.0/24"}, fake.LastCF())

	md := auditMeta(t, e, c, "domain.create", "loja.exemplo.net")
	assert.Equal(t, true, md["ok"])
	assert.Equal(t, float64(2), md["routes"])
	assert.Equal(t, "custom", md["tls"])

	// Listagem com o mesmo formato.
	w = e.do(t, http.MethodGet, "/api/domains", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code)
	ld := decode(t, w)["domains"].([]any)[0].(map[string]any)
	assert.Len(t, ld["routes"], 2)
	assert.Equal(t, "loja", ld["tls"].(map[string]any)["certName"])

	// Erro com código de campo, auditado sem gravar.
	bad := map[string]any{"hostname": "x.exemplo.com", "routes": []any{
		map[string]any{"path": "/", "targetType": "static", "root": "/var/www-evil"}}}
	w = e.do(t, http.MethodPost, "/api/domains", bad, mut(c, csrf)...)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	er := decode(t, w)["error"].(map[string]any)
	assert.Equal(t, "INVALID_ROOT", er["code"])
	assert.Equal(t, "routes[0].root", er["field"])
	md = auditMeta(t, e, c, "domain.create", "x.exemplo.com")
	assert.Equal(t, false, md["ok"])
	assert.Equal(t, float64(1), md["routes"])
}

func TestAPI_OldStyleBodyStillWorks(t *testing.T) {
	e, _, fake, c, csrf := domainsEnv(t, "203.0.113.10")
	w := e.do(t, http.MethodPost, "/api/domains", addrDomain("app.exemplo.com"), mut(c, csrf)...)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	d := decode(t, w)["domain"].(map[string]any)
	assert.Equal(t, "address", d["targetType"])
	assert.Equal(t, "127.0.0.1:5000", d["address"])
	assert.Nil(t, d["appId"])
	assert.Equal(t, true, d["compression"])
	assert.Equal(t, false, d["cloudflareOnly"])
	assert.Equal(t, []any{}, d["headers"])
	assert.Len(t, d["routes"], 1)
	assert.Equal(t, map[string]any{"mode": "auto", "certId": nil, "expiresSoon": false}, d["tls"])
	assert.Equal(t, []proxy.SiteRoute{{Path: "/", Upstream: "127.0.0.1:5000"}}, fake.LastApplied()[0].Routes)

	md := auditMeta(t, e, c, "domain.create", "app.exemplo.com")
	assert.Equal(t, float64(1), md["routes"])
	assert.Equal(t, "auto", md["tls"])
}

func TestPanelDomain_CustomCertAndCloudflareOnly(t *testing.T) {
	e, svc, fake, c, csrf := domainsEnv(t, "203.0.113.10")
	ctx := context.Background()
	require.NoError(t, svc.SetPanelDomain(ctx, "painel.exemplo.com"))
	other, err := svc.Certs.Add(ctx, "outro", []byte(genCertPEM(t, "loja.exemplo.net", time.Now().Add(90*24*time.Hour))))
	require.NoError(t, err)
	cert, err := svc.Certs.Add(ctx, "painel", []byte(genCertPEM(t, "painel.exemplo.com", time.Now().Add(90*24*time.Hour))))
	require.NoError(t, err)

	// Certificado que não cobre o hostname do painel.
	body := map[string]any{"hostname": "painel.exemplo.com", "tls": map[string]any{"mode": "custom", "certId": other.ID}}
	w := e.do(t, http.MethodPut, "/api/domains/1", body, mut(c, csrf)...)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Equal(t, "CERT_MISMATCH", errCode(decode(t, w)))

	body = map[string]any{"hostname": "painel.exemplo.com", "tls": map[string]any{"mode": "custom", "certId": cert.ID},
		"cloudflareOnly": true, "headers": []any{map[string]any{"name": "X-Frame-Options", "value": "DENY"}}, "compression": false}
	w = e.do(t, http.MethodPut, "/api/domains/1", body, mut(c, csrf)...)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	d := decode(t, w)["domain"].(map[string]any)
	assert.Equal(t, true, d["isPanel"])
	assert.Equal(t, true, d["cloudflareOnly"])
	assert.Equal(t, "custom", d["tls"].(map[string]any)["mode"])
	assert.Equal(t, "127.0.0.1:8000", d["address"])
	site := fake.LastApplied()[0]
	assert.Equal(t, "painel.exemplo.com", site.Host)
	assert.True(t, site.CloudflareOnly)
	assert.Equal(t, "custom", site.TLS.Mode)
	assert.False(t, site.Compression)
	assert.Equal(t, []proxy.SiteRoute{{Path: "/", Upstream: "127.0.0.1:8000"}}, site.Routes)

	// As rotas do painel são fixas.
	body["routes"] = []any{map[string]any{"path": "/", "targetType": "address", "address": "127.0.0.1:5000"}}
	w = e.do(t, http.MethodPut, "/api/domains/1", body, mut(c, csrf)...)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	er := decode(t, w)["error"].(map[string]any)
	assert.Equal(t, "INVALID_ROUTE", er["code"])
	assert.Equal(t, "routes", er["field"])

	// Remover continua protegido.
	w = e.do(t, http.MethodDelete, "/api/domains/1", nil, mut(c, csrf)...)
	assert.Equal(t, http.StatusConflict, w.Code)
}

// createRich cria loja.exemplo.net com duas rotas, certificado próprio, só
// Cloudflare e um cabeçalho; devolve o id do certificado.
func createRich(t *testing.T, e *env, svc *domains.Service, c *http.Cookie, csrf string) int64 {
	t.Helper()
	cert, err := svc.Certs.Add(context.Background(), "loja", []byte(genCertPEM(t, "loja.exemplo.net", time.Now().Add(90*24*time.Hour))))
	require.NoError(t, err)
	body := map[string]any{
		"hostname": "loja.exemplo.net", "forceHttps": true,
		"routes": []any{
			map[string]any{"path": "/", "targetType": "address", "address": "127.0.0.1:5000", "maxBodyBytes": 2048},
			map[string]any{"path": "/api/", "targetType": "address", "address": "127.0.0.1:6000"},
		},
		"tls":            map[string]any{"mode": "custom", "certId": cert.ID},
		"cloudflareOnly": true,
		"headers":        []any{map[string]any{"name": "X-Frame-Options", "value": "DENY"}},
	}
	w := e.do(t, http.MethodPost, "/api/domains", body, mut(c, csrf)...)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	return cert.ID
}

func TestAPI_UpdateOnlyCompressionKeepsEverythingElse(t *testing.T) {
	e, svc, _, c, csrf := domainsEnv(t, "203.0.113.10")
	certID := createRich(t, e, svc, c, csrf)
	w := e.do(t, http.MethodPut, "/api/domains/1", map[string]any{"compression": false}, mut(c, csrf)...)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	d := decode(t, w)["domain"].(map[string]any)
	assert.Equal(t, false, d["compression"])
	assert.Equal(t, "loja.exemplo.net", d["hostname"])
	assert.Equal(t, true, d["forceHttps"])
	assert.Equal(t, true, d["cloudflareOnly"])
	assert.Equal(t, "custom", d["tls"].(map[string]any)["mode"])
	assert.Equal(t, float64(certID), d["tls"].(map[string]any)["certId"])
	assert.Len(t, d["headers"], 1)
	require.Len(t, d["routes"], 2)
	assert.Equal(t, float64(2048), d["routes"].([]any)[0].(map[string]any)["maxBodyBytes"])
}

func TestAPI_LegacyPutReplacesOnlyRootTarget(t *testing.T) {
	e, svc, _, c, csrf := domainsEnv(t, "203.0.113.10")
	createRich(t, e, svc, c, csrf)
	in := addrDomain("loja.exemplo.net")
	in["address"] = "127.0.0.1:5001"
	w := e.do(t, http.MethodPut, "/api/domains/1", in, mut(c, csrf)...)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	d := decode(t, w)["domain"].(map[string]any)
	assert.Equal(t, "127.0.0.1:5001", d["address"])
	routes := d["routes"].([]any)
	require.Len(t, routes, 2)
	assert.Equal(t, "/", routes[0].(map[string]any)["path"])
	assert.Equal(t, "127.0.0.1:5001", routes[0].(map[string]any)["address"])
	assert.Equal(t, float64(2048), routes[0].(map[string]any)["maxBodyBytes"])
	assert.Equal(t, "/api/", routes[1].(map[string]any)["path"])
	assert.Equal(t, "127.0.0.1:6000", routes[1].(map[string]any)["address"])
	assert.Equal(t, true, d["cloudflareOnly"])

	// Rota "/" estática: o corpo antigo que repete targetType static a mantém.
	body := map[string]any{"hostname": "site.exemplo.com", "routes": []any{
		map[string]any{"path": "/", "targetType": "static", "root": "/var/www/site", "spa": true}}}
	w = e.do(t, http.MethodPost, "/api/domains", body, mut(c, csrf)...)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	id := decode(t, w)["domain"].(map[string]any)["id"]
	legacy := map[string]any{"hostname": "site.exemplo.com", "targetType": "static", "address": "", "forceHttps": true}
	w = e.do(t, http.MethodPut, fmt.Sprintf("/api/domains/%v", id), legacy, mut(c, csrf)...)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	r0 := decode(t, w)["domain"].(map[string]any)["routes"].([]any)[0].(map[string]any)
	assert.Equal(t, "static", r0["targetType"])
	assert.Equal(t, "/var/www/site", r0["root"])
	assert.Equal(t, true, r0["spa"])
}

func TestPanelDomain_PartialPutAndPortOff(t *testing.T) {
	e, svc, fake, c, csrf := domainsEnv(t, "203.0.113.10")
	ctx := context.Background()
	require.NoError(t, svc.SetPanelDomain(ctx, "painel.exemplo.com"))
	cert, err := svc.Certs.Add(ctx, "painel", []byte(genCertPEM(t, "painel.exemplo.com", time.Now().Add(90*24*time.Hour))))
	require.NoError(t, err)
	cert2, err := svc.Certs.Add(ctx, "painel2", []byte(genCertPEM(t, "painel.exemplo.com", time.Now().Add(60*24*time.Hour))))
	require.NoError(t, err)
	body := map[string]any{"tls": map[string]any{"mode": "custom", "certId": cert.ID}, "cloudflareOnly": true}
	w := e.do(t, http.MethodPut, "/api/domains/1", body, mut(c, csrf)...)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	// Só compressão: certificado e só Cloudflare mantidos.
	w = e.do(t, http.MethodPut, "/api/domains/1", map[string]any{"compression": false}, mut(c, csrf)...)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	d := decode(t, w)["domain"].(map[string]any)
	assert.Equal(t, true, d["cloudflareOnly"])
	assert.Equal(t, float64(cert.ID), d["tls"].(map[string]any)["certId"])
	assert.Equal(t, false, d["compression"])
	assert.True(t, fake.LastApplied()[0].CloudflareOnly)

	// Porta pública desligada: trocar o certificado passa; ligar o só
	// Cloudflare é recusado.
	require.NoError(t, svc.DB.SetSetting(ctx, domains.SettingPublicPort, "false"))
	w = e.do(t, http.MethodPut, "/api/domains/1", map[string]any{"tls": map[string]any{"mode": "custom", "certId": cert2.ID}}, mut(c, csrf)...)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, float64(cert2.ID), decode(t, w)["domain"].(map[string]any)["tls"].(map[string]any)["certId"])
	w = e.do(t, http.MethodPut, "/api/domains/1", map[string]any{"cloudflareOnly": false}, mut(c, csrf)...)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = e.do(t, http.MethodPut, "/api/domains/1", map[string]any{"cloudflareOnly": true}, mut(c, csrf)...)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Equal(t, "PANEL_PORT_OFF", errCode(decode(t, w)))
}
