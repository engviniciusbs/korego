package api_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/audit"
	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/certs"
	"github.com/engviniciusbs/korego/internal/domains"
	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/github"
	"github.com/engviniciusbs/korego/internal/migrate"
	"github.com/engviniciusbs/korego/internal/node/nodetest"
	"github.com/engviniciusbs/korego/internal/node/proxy/proxytest"
	"github.com/engviniciusbs/korego/internal/store"
)

const (
	tokenHTTPSMsg = "Tokens de API só funcionam via HTTPS. Configure o domínio do painel em Configurações → Painel."
	agentAddr     = "198.51.100.7:4000"
)

func withTLS() reqOpt { return func(r *http.Request) { r.TLS = &tls.ConnectionState{} } }

func bearer(s string) reqOpt { return withHeader("Authorization", "Bearer "+s) }

// probeSeen é o que o handler de prova viu no contexto.
type probeSeen struct {
	calls   int
	user    store.User
	userOK  bool
	sess    store.Session
	ref     audit.TokenRef
	refOK   bool
	idTok   store.APIToken
	idAddr  string
	idHdr   http.Header
	idOK    bool
	reqAuth string
}

// tokenProbe monta RequireToken na frente de um handler que registra o contexto.
type tokenProbe struct {
	*env
	tokens *auth.Tokens
	lim    *auth.Limiter
	h      http.Handler
	seen   probeSeen
	secret string // TOTP
	tok    store.APIToken
	token  string // segredo do token
}

// newTokenProbe: shareLoginLimiter usa o Limiter do login também para tokens.
func newTokenProbe(t *testing.T, shareLoginLimiter bool) *tokenProbe {
	t.Helper()
	p := &tokenProbe{env: newEnv(t)}
	p.secret, _, _ = p.setup(t)
	p.tokens = &auth.Tokens{DB: p.svc.DB, Now: p.svc.Now}
	p.lim = auth.NewLimiter(5, time.Minute, 15*time.Minute)
	if shareLoginLimiter {
		p.lim = p.svc.Limiter
	}
	p.h = api.TokenProbeForTest(p.tokens, p.svc.DB, p.lim, func(w http.ResponseWriter, r *http.Request) {
		s := &p.seen
		s.calls++
		s.user, s.userOK = api.CurrentUser(r.Context())
		s.sess, _ = api.CurrentSession(r.Context())
		s.ref, s.refOK = audit.TokenFrom(r.Context())
		s.idTok, _, s.idAddr, s.idHdr, s.idOK = api.TokenIdentityForTest(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	u, err := p.svc.DB.UserByEmail(context.Background(), email)
	require.NoError(t, err)
	p.token, p.tok, err = p.tokens.Issue(context.Background(), u.ID, "claude-code", 0)
	require.NoError(t, err)
	return p
}

func (p *tokenProbe) call(t *testing.T, path string, opts ...reqOpt) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, nil)
	r.RemoteAddr = "203.0.113.5:1"
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	p.h.ServeHTTP(w, r)
	return w
}

func TestRequireToken_Valid(t *testing.T) {
	p := newTokenProbe(t, false)
	w := p.call(t, "/mcp", withTLS(), bearer(p.token),
		withHeader("User-Agent", "claude-code/1.0"), withHeader("Cookie", "korego_session=x"))
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	s := p.seen
	require.True(t, s.userOK)
	assert.Equal(t, email, s.user.Email)
	assert.Equal(t, s.user.ID, s.sess.UserID)
	assert.Equal(t, "token:"+itoa(p.tok.ID), s.sess.ID)
	require.True(t, s.refOK)
	assert.Equal(t, audit.TokenRef{ID: p.tok.ID, Name: "claude-code"}, s.ref)
	require.True(t, s.idOK)
	assert.Equal(t, p.tok.ID, s.idTok.ID)
	assert.Equal(t, "203.0.113.5:1", s.idAddr)
	// Só os cabeçalhos de identidade são guardados; nada de Authorization ou Cookie.
	assert.Equal(t, "claude-code/1.0", s.idHdr.Get("User-Agent"))
	assert.Empty(t, s.idHdr.Get("Authorization"))
	assert.Empty(t, s.idHdr.Get("Cookie"))
}

func TestRequireToken_SchemeVariants(t *testing.T) {
	p := newTokenProbe(t, false)
	for _, h := range []string{"bearer " + p.token, "Bearer   " + p.token, "BEARER " + p.token} {
		w := p.call(t, "/mcp", withTLS(), withHeader("Authorization", h))
		assert.Equal(t, http.StatusNoContent, w.Code, h)
	}
	// Cada recusa de um IP diferente, para não chegar ao limite (5).
	for i, h := range []string{"", p.token, "Basic " + p.token, "Bearer", "Bearer " + p.token + " extra"} {
		w := p.call(t, "/mcp", withTLS(), withRemote("203.0.113."+strconv.Itoa(100+i)+":1"), withHeader("Authorization", h))
		assert.Equal(t, http.StatusUnauthorized, w.Code, h)
		assert.Equal(t, "INVALID_TOKEN", errCode(decode(t, w)), h)
	}
	w := p.call(t, "/mcp?token="+p.token, withTLS())
	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "INVALID_TOKEN", errCode(decode(t, w)))
	assert.Equal(t, 3, p.seen.calls)
}

func TestRequireToken_Transport(t *testing.T) {
	p := newTokenProbe(t, false)
	cases := []struct {
		name string
		opts []reqOpt
		ok   bool
	}{
		{"remoto sem TLS", []reqOpt{withRemote("203.0.113.5:1")}, false},
		{"remoto com X-Forwarded-Proto forjado", []reqOpt{withRemote("203.0.113.5:1"), withHeader("X-Forwarded-Proto", "https")}, false},
		{"remoto com TLS", []reqOpt{withRemote("203.0.113.5:1"), withTLS()}, true},
		{"loopback direto", []reqOpt{withRemote("127.0.0.1:1")}, true},
		{"loopback IPv6 direto", []reqOpt{withRemote("[::1]:1")}, true},
		{"loopback via proxy http", []reqOpt{withRemote("127.0.0.1:1"), withHeader("X-Forwarded-Proto", "http")}, false},
		{"loopback via proxy https", []reqOpt{withRemote("127.0.0.1:1"), withHeader("X-Forwarded-Proto", "https")}, true},
		{"loopback com X-Forwarded-For", []reqOpt{withRemote("127.0.0.1:1"), withHeader("X-Forwarded-For", "203.0.113.5")}, false},
		{"loopback com X-Forwarded-Host", []reqOpt{withRemote("127.0.0.1:1"), withHeader("X-Forwarded-Host", "x")}, false},
		{"loopback com Forwarded", []reqOpt{withRemote("127.0.0.1:1"), withHeader("Forwarded", "proto=http")}, false},
	}
	for _, c := range cases {
		w := p.call(t, "/mcp", append(c.opts, bearer(p.token))...)
		if c.ok {
			assert.Equal(t, http.StatusNoContent, w.Code, c.name)
			continue
		}
		require.Equal(t, http.StatusForbidden, w.Code, c.name)
		assert.JSONEq(t, `{"error":{"code":"TOKEN_REQUIRES_HTTPS","message":"`+tokenHTTPSMsg+`"}}`, w.Body.String(), c.name)
	}
}

func TestRequireToken_RevokedMidway(t *testing.T) {
	p := newTokenProbe(t, false)
	require.Equal(t, http.StatusNoContent, p.call(t, "/mcp", withTLS(), bearer(p.token)).Code)
	_, err := p.svc.DB.RevokeAPIToken(context.Background(), p.tok.UserID, p.tok.ID, *p.now)
	require.NoError(t, err)
	w := p.call(t, "/mcp", withTLS(), bearer(p.token))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "INVALID_TOKEN", errCode(decode(t, w)))
	assert.Equal(t, 1, p.seen.calls)
}

func TestRequireToken_LimiterSeparate(t *testing.T) {
	// O mesmo Limiter do login: só o espaço de chaves "token:" separa os dois.
	p := newTokenProbe(t, true)
	from := withRemote(remoteIP + ":1")
	for i := 0; i < 5; i++ {
		w := p.call(t, "/mcp", withTLS(), from, bearer("kg_"+strings.Repeat("A", 43)))
		require.Equal(t, http.StatusUnauthorized, w.Code, i)
	}
	w := p.call(t, "/mcp", withTLS(), from, bearer("kg_"+strings.Repeat("A", 43)))
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "RATE_LIMITED", errCode(decode(t, w)))
	// Bloqueado vale também para um token válido do mesmo IP.
	assert.Equal(t, http.StatusTooManyRequests, p.call(t, "/mcp", withTLS(), from, bearer(p.token)).Code)
	// Outro IP segue livre.
	assert.Equal(t, http.StatusNoContent, p.call(t, "/mcp", withTLS(), withRemote("203.0.113.77:1"), bearer(p.token)).Code)

	// O login do painel do mesmo IP não é bloqueado.
	w = p.login(t, p.secret, from)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func TestAPI_IgnoresBearer(t *testing.T) {
	p := newTokenProbe(t, false)
	w := p.do(t, http.MethodGet, "/api/system", nil, bearer(p.token), withTLS())
	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "UNAUTHORIZED", errCode(decode(t, w)))
}

// gateFx tem todas as dependências do núcleo, para as rotas protegidas por 2FA.
type gateFx struct {
	*appsFx
	upd  *fakeUpdater
	disp api.DispatcherForTest
	tok  store.APIToken
	user store.User
}

func newGateFx(t *testing.T) *gateFx {
	t.Helper()
	g := &gateFx{upd: &fakeUpdater{current: "0.4.0", latest: "v0.5.0"}}
	g.appsFx = newAppsFx(t, func(f *appsFx, d *api.Deps) {
		px := &proxytest.Fake{}
		dom := &domains.Service{DB: f.db, Proxy: px, Hub: events.NewHub(), PanelPort: 8000, ListenAddr: ":8000"}
		d.Domains = dom
		d.Migrate = &migrate.Engine{Domains: dom, Proxy: px, Systemd: &fakeUnits{active: map[string]bool{}}, DB: f.db, Hub: dom.Hub}
		d.Certs = &certs.Service{DB: f.db, Box: d.Box}
		d.Node = &nodetest.Fake{DockerClient: f.dock}
		d.Updater = g.upd
		d.GitHub = &github.Service{DB: f.db, Box: d.Box, Client: &github.Client{}}
		d.TokenLimiter = auth.NewLimiter(5, time.Minute, 15*time.Minute)
	})
	ctx := context.Background()
	u, err := g.env.svc.DB.UserByEmail(ctx, email)
	require.NoError(t, err)
	g.user = u
	tokens := &auth.Tokens{DB: g.env.svc.DB, Now: g.env.svc.Now}
	_, g.tok, err = tokens.Issue(ctx, u.ID, "agente-ci", 0)
	require.NoError(t, err)
	g.disp = api.NewDispatcherForTest(g.env.deps)
	return g
}

// as faz a chamada pelo despacho interno, como o token.
func (g *gateFx) as(t *testing.T, method, path string, body any, code string) (int, map[string]any) {
	t.Helper()
	st, b := g.disp.Do(context.Background(), g.tok, g.user, agentAddr, http.Header{}, method, path, nil, body, code)
	if len(b) == 0 {
		return st, nil
	}
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m), string(b))
	return st, m
}

func (g *gateFx) freshCode(t *testing.T) string {
	*g.env.now = g.env.now.Add(30 * time.Second)
	return g.env.code(t, g.totp)
}

// audits devolve a metadata das entradas de auditoria com action.
func audits(t *testing.T, e *env, c *http.Cookie, action string) []map[string]any {
	t.Helper()
	w := e.do(t, http.MethodGet, "/api/audit?limit=100", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code)
	var out []map[string]any
	for _, it := range decode(t, w)["items"].([]any) {
		m := it.(map[string]any)
		if m["action"] == action {
			md, _ := m["metadata"].(map[string]any)
			out = append(out, md)
		}
	}
	return out
}

func TestGate_TokenNeedsCode(t *testing.T) {
	g := newGateFx(t)
	pid := g.project(t)

	st, b := g.as(t, http.MethodDelete, "/api/projects/"+itoa(pid), nil, "")
	require.Equal(t, http.StatusForbidden, st, b)
	assert.Equal(t, "CODE_REQUIRED", errCode(b))
	_, err := g.db.GetProject(context.Background(), pid)
	require.NoError(t, err, "o handler não pode ter rodado")

	st, b = g.as(t, http.MethodDelete, "/api/projects/"+itoa(pid), nil, "000000")
	require.Equal(t, http.StatusForbidden, st, b)
	assert.Equal(t, "INVALID_CODE", errCode(b))

	code := g.freshCode(t)
	st, b = g.as(t, http.MethodDelete, "/api/projects/"+itoa(pid), nil, code)
	require.Equal(t, http.StatusNoContent, st, b)

	pid2 := g.project(t)
	st, b = g.as(t, http.MethodDelete, "/api/projects/"+itoa(pid2), nil, code)
	require.Equal(t, http.StatusForbidden, st, b)
	assert.Equal(t, "INVALID_CODE", errCode(b))

	// As recusas são auditadas com o token.
	stages := map[string]int{}
	for _, md := range audits(t, g.env, g.cookie, "api_token.code") {
		stages[md["stage"].(string)]++
		assert.Equal(t, "token", md["via"])
		assert.Equal(t, "agente-ci", md["token"])
	}
	assert.Equal(t, map[string]int{"code_required": 1, "invalid_code": 2}, stages)

	// Toda rota protegida pede o código ao token, antes do handler.
	gated := []struct{ method, path string }{
		{http.MethodDelete, "/api/apps/1"},
		{http.MethodDelete, "/api/projects/" + itoa(pid2)},
		{http.MethodDelete, "/api/domains/1"},
		{http.MethodDelete, "/api/certificates/1"},
		{http.MethodDelete, "/api/containers/" + cid},
		{http.MethodPost, "/api/proxy/takeover"},
		{http.MethodPost, "/api/proxy/rollback"},
		{http.MethodPost, "/api/update"},
		{http.MethodPut, "/api/settings/panel/domain"},
		{http.MethodPut, "/api/settings/panel/public-port"},
		{http.MethodDelete, "/api/github"},
	}
	for _, c := range gated {
		st, b := g.as(t, c.method, c.path, nil, "")
		assert.Equal(t, http.StatusForbidden, st, c.method+" "+c.path)
		if b != nil {
			assert.Equal(t, "CODE_REQUIRED", errCode(b), c.method+" "+c.path)
		}
	}
	_, err = g.db.GetProject(context.Background(), pid2)
	require.NoError(t, err)
	assert.Equal(t, 0, g.upd.applies)

	// Rotas não protegidas seguem sem código.
	st, b = g.as(t, http.MethodPost, "/api/projects", map[string]any{"name": "livre"}, "")
	assert.Equal(t, http.StatusCreated, st, b)
}

func TestGate_LimiterCountsWrongCodes(t *testing.T) {
	g := newGateFx(t)
	pid := g.project(t)
	for i := 0; i < 5; i++ {
		st, b := g.as(t, http.MethodDelete, "/api/projects/"+itoa(pid), nil, "000000")
		require.Equal(t, http.StatusForbidden, st, b)
	}
	st, b := g.as(t, http.MethodDelete, "/api/projects/"+itoa(pid), nil, g.freshCode(t))
	require.Equal(t, http.StatusTooManyRequests, st, b)
	assert.Equal(t, "RATE_LIMITED", errCode(b))
	assert.Len(t, audits(t, g.env, g.cookie, "api_token.code"), 6)
	// O login do painel segue livre.
	assert.Equal(t, http.StatusOK, g.env.login(t, g.totp).Code)
}

func TestGate_CookieUnchanged(t *testing.T) {
	g := newGateFx(t)
	pid := g.project(t)
	st, b := g.call(t, http.MethodDelete, "/api/projects/"+itoa(pid), nil)
	require.Equal(t, http.StatusNoContent, st, b)

	m := newMigEnv(t)
	b = m.post(t, "/api/proxy/takeover", nil)
	assert.EqualValues(t, http.StatusForbidden, b["_status"])
	assert.Equal(t, "CODE_REQUIRED", errCode(b))
	assert.Empty(t, audits(t, m.e, m.cookie, "api_token.code"))
}

func TestGate_TakeoverTokenUsesHeader(t *testing.T) {
	m := newMigEnv(t)
	ctx := context.Background()
	u, err := m.e.svc.DB.UserByEmail(ctx, email)
	require.NoError(t, err)
	_, tok, err := (&auth.Tokens{DB: m.e.svc.DB, Now: m.e.svc.Now}).Issue(ctx, u.ID, "agente", 0)
	require.NoError(t, err)
	disp := api.NewDispatcherForTest(m.e.deps)

	st, b := disp.Do(ctx, tok, u, agentAddr, http.Header{}, http.MethodPost, "/api/proxy/takeover", nil, nil, m.freshCode(t))
	require.Equal(t, http.StatusAccepted, st, string(b))
	m.waitIdle(t)
	assert.False(t, m.units.isActive("nginx"))

	var started map[string]any
	for _, md := range m.auditActions(t, "proxy.takeover") {
		if md["stage"] == "started" {
			started = md
		}
	}
	require.NotNil(t, started)
	assert.Equal(t, "token", started["via"])
	assert.Equal(t, "agente", started["token"])
}

func TestAudit_TokenLabel(t *testing.T) {
	g := newGateFx(t)
	st, b := g.as(t, http.MethodPost, "/api/projects", map[string]any{"name": "via-agente"}, "")
	require.Equal(t, http.StatusCreated, st, b)
	md := auditMeta(t, g.env, g.cookie, "project.create", "via-agente")
	assert.Equal(t, "token", md["via"])
	assert.Equal(t, "agente-ci", md["token"])
	assert.EqualValues(t, g.tok.ID, md["tokenId"])
	assert.Equal(t, true, md["ok"])
}

func TestAudit_ProxyRetry(t *testing.T) {
	e, _, _, c, csrf := domainsEnv(t, "203.0.113.10")
	w := e.do(t, http.MethodPost, "/api/proxy/retry", nil, mut(c, csrf)...)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	md := auditMeta(t, e, c, "proxy.retry", "proxy")
	assert.Equal(t, true, md["ok"])
	assert.NotEmpty(t, md["state"])
}

func TestDispatch_CarriesIdentityAndQuery(t *testing.T) {
	g := newGateFx(t)
	hdr := http.Header{"User-Agent": {"claude-code/1.0"}}
	st, b := g.disp.Do(context.Background(), g.tok, g.user, agentAddr, hdr, http.MethodGet, "/api/audit",
		map[string][]string{"limit": {"1"}}, nil, "")
	require.Equal(t, http.StatusOK, st, string(b))
	var out struct{ Items []any }
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Len(t, out.Items, 1)

	// Rota só de sessão e rota inexistente: 404 em JSON.
	for _, p := range []string{"/api/auth/me", "/api/auth/sessions", "/api/nada"} {
		st, b := g.as(t, http.MethodGet, p, nil, "")
		assert.Equal(t, http.StatusNotFound, st, p)
		assert.Equal(t, "NOT_FOUND", errCode(b), p)
	}
}
