package api_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/domains"
	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/github"
	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/node/proxy/proxytest"
	"github.com/engviniciusbs/korego/internal/store"
)

const (
	webhookSecret = "segredo-de-teste-do-webhook"
	ghWebURL      = "https://github.exemplo.com"
)

var (
	keyOnce sync.Once
	keyPEM  string
)

func testPEM(t *testing.T) string {
	t.Helper()
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
	})
	return keyPEM
}

// failTokens faz os deploys github falharem logo ao pedir o token (sem git).
type failTokens struct{}

func (failTokens) CloneToken(context.Context, string) (string, error) {
	return "", errors.New("sem rede no teste")
}

type ghFx struct {
	*appsFx
	ghs         *github.Service
	dom         *domains.Service
	now         time.Time
	conversions int32
	instFail    atomic.Bool
}

// newGHFx monta a API com o GitHub App apontando para um GitHub falso.
func newGHFx(t *testing.T, panelReady bool) *ghFx {
	t.Helper()
	g := &ghFx{now: time.Unix(1_800_000_000, 0)}
	pemKey := testPEM(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app-manifests/{code}/conversions", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&g.conversions, 1)
		if r.PathValue("code") == "ruim" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 42, "slug": "korego-vps", "pem": pemKey, "webhook_secret": webhookSecret,
			"html_url": "https://github.exemplo.com/apps/korego-vps", "owner": map[string]any{"login": "exemplo"},
		})
	})
	mux.HandleFunc("GET /app/installations", func(w http.ResponseWriter, _ *http.Request) {
		if g.instFail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`[{"id":1,"account":{"login":"exemplo"}}]`))
	})
	mux.HandleFunc("POST /app/installations/1/access_tokens", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "tok1", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	})
	mux.HandleFunc("GET /installation/repositories", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"repositories":[{"full_name":"exemplo/site","default_branch":"main","private":true}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	fake := &proxytest.Fake{}
	if panelReady {
		fake.CertFn = func(context.Context, string) proxy.CertInfo { return proxy.CertInfo{State: proxy.CertActive} }
	}
	g.appsFx = newAppsFx(t, func(f *appsFx, d *api.Deps) {
		f.svc.Tokens = failTokens{}
		g.ghs = &github.Service{DB: f.db, Box: d.Box, Client: &github.Client{APIURL: srv.URL, WebURL: ghWebURL}}
		g.dom = &domains.Service{
			DB: f.db, Proxy: fake, Hub: events.NewHub(),
			Resolver:  fakeResolver{ips: []net.IP{net.ParseIP("203.0.113.10")}},
			HostIPs:   func() ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.10")}, nil },
			PanelPort: 8000, ListenAddr: ":8000",
		}
		d.GitHub = g.ghs
		d.Domains = g.dom
		d.Now = func() time.Time { return g.now }
		d.Hostname = func() (string, error) { return "vps-exemplo", nil }
	})
	require.NoError(t, g.dom.SetPanelDomain(context.Background(), "painel.exemplo.com"))
	return g
}

// manifest pede o manifesto e devolve o state da URL de ação.
func (g *ghFx) manifest(t *testing.T, org string) string {
	t.Helper()
	code, b := g.call(t, http.MethodPost, "/api/github/manifest", map[string]any{"org": org})
	require.Equal(t, http.StatusOK, code, b)
	u, err := url.Parse(b["action"].(string))
	require.NoError(t, err)
	s := u.Query().Get("state")
	require.Len(t, s, 64)
	return s
}

// callback simula o retorno do github.com: sem cookie nem CSRF.
func (g *ghFx) callback(t *testing.T, code, state string) string {
	t.Helper()
	w := g.do(t, http.MethodGet, "/api/github/callback?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(state), nil)
	require.Equal(t, http.StatusFound, w.Code, w.Body.String())
	return w.Header().Get("Location")
}

func (g *ghFx) connect(t *testing.T) {
	t.Helper()
	require.Equal(t, "/configuracoes/github/?conectado=1", g.callback(t, "abc123", g.manifest(t, "")))
}

func auditItems(t *testing.T, e *env, c *http.Cookie, action string) []map[string]any {
	t.Helper()
	w := e.do(t, http.MethodGet, "/api/audit?limit=200", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code)
	var out []map[string]any
	for _, it := range decode(t, w)["items"].([]any) {
		m := it.(map[string]any)
		if m["action"] == action {
			out = append(out, m)
		}
	}
	return out
}

func TestManifest_RequiresPanelHTTPS(t *testing.T) {
	g := newGHFx(t, false)
	code, b := g.call(t, http.MethodPost, "/api/github/manifest", map[string]any{"org": ""})
	require.Equal(t, http.StatusConflict, code, b)
	assert.Equal(t, "PANEL_NOT_HTTPS", errCode(b))

	code, b = g.call(t, http.MethodGet, "/api/github", nil)
	require.Equal(t, http.StatusOK, code, b)
	assert.Equal(t, false, b["connected"])
	assert.Nil(t, b["app"])
	assert.Equal(t, []any{}, b["installations"])
	assert.Equal(t, false, b["panelReady"])

	// Rotas de gestão exigem sessão e CSRF.
	w := g.do(t, http.MethodGet, "/api/github", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	w = g.do(t, http.MethodPost, "/api/github/manifest", map[string]any{"org": ""}, withCookie(g.cookie))
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestManifest_ActionAndOrg(t *testing.T) {
	g := newGHFx(t, true)
	_, b := g.call(t, http.MethodGet, "/api/github", nil)
	assert.Equal(t, true, b["panelReady"])

	code, b := g.call(t, http.MethodPost, "/api/github/manifest", map[string]any{"org": ""})
	require.Equal(t, http.StatusOK, code, b)
	action := b["action"].(string)
	assert.True(t, strings.HasPrefix(action, ghWebURL+"/settings/apps/new?state="), action)
	assert.Equal(t, string(github.Manifest("https://painel.exemplo.com", "vps-exemplo")), b["manifest"])

	code, b = g.call(t, http.MethodPost, "/api/github/manifest", map[string]any{"org": "Minha-Org"})
	require.Equal(t, http.StatusOK, code, b)
	assert.True(t, strings.HasPrefix(b["action"].(string), ghWebURL+"/organizations/Minha-Org/settings/apps/new?state="))

	for _, bad := range []string{"a/b", "org inválida", strings.Repeat("a", 40), "../x"} {
		code, b = g.call(t, http.MethodPost, "/api/github/manifest", map[string]any{"org": bad})
		require.Equal(t, http.StatusBadRequest, code, bad)
		assert.Equal(t, "org", b["error"].(map[string]any)["field"])
	}
}

func TestCallback_WorksWithoutCookie(t *testing.T) {
	g := newGHFx(t, true)
	state := g.manifest(t, "")
	loc := g.callback(t, "abc123", state)
	assert.Equal(t, "/configuracoes/github/?conectado=1", loc)

	a, err := g.db.GetGitHubApp(context.Background())
	require.NoError(t, err)
	assert.EqualValues(t, 42, a.AppID)
	assert.Equal(t, "korego-vps", a.Slug)
	assert.Equal(t, "exemplo", a.Owner)
	assert.NotContains(t, a.PrivateKeyEnc, "PRIVATE KEY")
	assert.NotEqual(t, webhookSecret, a.WebhookSecretEnc)
	pemB, err := g.ghs.Box.Decrypt(a.PrivateKeyEnc)
	require.NoError(t, err)
	assert.Equal(t, testPEM(t), string(pemB))
	sec, err := g.ghs.Box.Decrypt(a.WebhookSecretEnc)
	require.NoError(t, err)
	assert.Equal(t, webhookSecret, string(sec))

	// Auditoria com o usuário que gerou o state, sem segredos.
	items := auditItems(t, g.env, g.cookie, "github.connect")
	require.Len(t, items, 1)
	assert.NotNil(t, items[0]["userId"])
	assert.Equal(t, "korego-vps", items[0]["target"])
	w := g.do(t, http.MethodGet, "/api/audit?limit=200", nil, withCookie(g.cookie))
	assert.NotContains(t, w.Body.String(), webhookSecret)
	assert.NotContains(t, w.Body.String(), "abc123")
}

func TestCallback_StateReuseExpiredUnknown(t *testing.T) {
	g := newGHFx(t, true)
	state := g.manifest(t, "")
	require.Equal(t, "/configuracoes/github/?conectado=1", g.callback(t, "abc123", state))

	// Reutilizado.
	assert.Equal(t, "/configuracoes/github/?erro=STATE_INVALID", g.callback(t, "abc123", state))
	// Desconhecido e ausente.
	assert.Equal(t, "/configuracoes/github/?erro=STATE_INVALID", g.callback(t, "abc123", strings.Repeat("ab", 32)))
	assert.Equal(t, "/configuracoes/github/?erro=STATE_INVALID", g.callback(t, "abc123", ""))

	// Expirado: desconecta, gera outro state e passa de 10 minutos.
	code, _ := g.call(t, http.MethodDelete, "/api/github", nil)
	require.Equal(t, http.StatusOK, code)
	before := atomic.LoadInt32(&g.conversions)
	state = g.manifest(t, "")
	g.now = g.now.Add(10*time.Minute + time.Second)
	assert.Equal(t, "/configuracoes/github/?erro=STATE_INVALID", g.callback(t, "abc123", state))
	// Expirado e consumido: nem volta a valer.
	g.now = g.now.Add(-time.Minute)
	assert.Equal(t, "/configuracoes/github/?erro=STATE_INVALID", g.callback(t, "abc123", state))
	assert.Equal(t, before, atomic.LoadInt32(&g.conversions), "state inválido não chega ao GitHub")
	_, err := g.db.GetGitHubApp(context.Background())
	assert.ErrorIs(t, err, store.ErrNotFound)

	// Dentro do prazo ainda funciona.
	state = g.manifest(t, "")
	g.now = g.now.Add(9 * time.Minute)
	assert.Equal(t, "/configuracoes/github/?conectado=1", g.callback(t, "abc123", state))
}

func TestCallback_AlreadyConnectedAndConversionFailed(t *testing.T) {
	g := newGHFx(t, true)
	bad := g.manifest(t, "")
	assert.Equal(t, "/configuracoes/github/?erro=CONVERSION_FAILED", g.callback(t, "ruim", bad))
	_, err := g.db.GetGitHubApp(context.Background())
	assert.ErrorIs(t, err, store.ErrNotFound)

	s1, s2 := g.manifest(t, ""), g.manifest(t, "")
	assert.Equal(t, "/configuracoes/github/?conectado=1", g.callback(t, "abc123", s1))
	assert.Equal(t, "/configuracoes/github/?erro=ALREADY_CONNECTED", g.callback(t, "abc123", s2))

	code, b := g.call(t, http.MethodPost, "/api/github/manifest", map[string]any{"org": ""})
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "ALREADY_CONNECTED", errCode(b))
}

func TestGitHubAPI_StatusReposDisconnect(t *testing.T) {
	g := newGHFx(t, true)
	code, b := g.call(t, http.MethodGet, "/api/github/repos", nil)
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "NO_GITHUB", errCode(b))

	g.connect(t)
	code, b = g.call(t, http.MethodGet, "/api/github", nil)
	require.Equal(t, http.StatusOK, code, b)
	assert.Equal(t, true, b["connected"])
	assert.Equal(t, map[string]any{"slug": "korego-vps", "owner": "exemplo",
		"htmlUrl":    "https://github.exemplo.com/apps/korego-vps",
		"installUrl": ghWebURL + "/apps/korego-vps/installations/new"}, b["app"])
	assert.Equal(t, []any{map[string]any{"id": float64(1), "account": "exemplo"}}, b["installations"])
	assert.NotContains(t, b, "installationsError")

	// Falha ao listar instalações não vira 500.
	g.instFail.Store(true)
	code, b = g.call(t, http.MethodGet, "/api/github", nil)
	require.Equal(t, http.StatusOK, code, b)
	assert.Equal(t, []any{}, b["installations"])
	assert.NotEmpty(t, b["installationsError"])
	g.instFail.Store(false)

	code, b = g.call(t, http.MethodGet, "/api/github/repos", nil)
	require.Equal(t, http.StatusOK, code, b)
	assert.Equal(t, []any{map[string]any{"fullName": "exemplo/site", "defaultBranch": "main", "installationId": float64(1)}}, b["repos"])

	code, b = g.call(t, http.MethodDelete, "/api/github", nil)
	require.Equal(t, http.StatusOK, code, b)
	assert.Equal(t, true, b["removed"])
	assert.Equal(t, "https://github.exemplo.com/apps/korego-vps", b["manageUrl"])
	assert.Len(t, auditItems(t, g.env, g.cookie, "github.disconnect"), 1)

	_, b = g.call(t, http.MethodGet, "/api/github", nil)
	assert.Equal(t, false, b["connected"])
	code, _ = g.call(t, http.MethodGet, "/api/github/repos", nil)
	assert.Equal(t, http.StatusConflict, code)
	code, b = g.call(t, http.MethodDelete, "/api/github", nil)
	require.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "NOT_CONNECTED", errCode(b))
}
