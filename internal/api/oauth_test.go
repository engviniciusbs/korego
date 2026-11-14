package api_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/auth"
)

const oauthOrigin = "https://p.exemplo.com"

// oauthFx monta o roteador com origem fixa; os pedidos vão por TLS.
type oauthFx struct {
	e    *env
	h    http.Handler
	skew *time.Duration // avanço do relógio do servidor OAuth (Deps.Now)
}

func newOAuthFx(t *testing.T, origin func(context.Context) (string, bool), maxUnauth int) *oauthFx {
	t.Helper()
	if origin == nil {
		origin = func(context.Context) (string, bool) { return oauthOrigin, true }
	}
	tk := &auth.Tokens{Key: auth.DeriveKey([]byte("chave-de-teste"), "oauth-refresh")}
	var tick int64
	skew := new(time.Duration)
	base := time.Now()
	e := newEnvDeps(t, api.Deps{Tokens: tk, OAuthOrigin: origin,
		Now: func() time.Time { tick++; return base.Add(time.Duration(tick)*time.Second + *skew) }})
	tk.DB, tk.Now = e.svc.DB, e.svc.Now
	return &oauthFx{e: e, h: api.OAuthRouterForTest(e.deps, maxUnauth), skew: skew}
}

func (f *oauthFx) do(method, path, body string, opts ...reqOpt) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	r.RemoteAddr = "203.0.113.5:1"
	withTLS()(r)
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

func (f *oauthFx) register(body string, opts ...reqOpt) *httptest.ResponseRecorder {
	return f.do(http.MethodPost, "/oauth/register", body, opts...)
}

const okReg = `{"client_name":"Teste","redirect_uris":["http://127.0.0.1:5000/cb"]}`

func TestOAuthMetadata(t *testing.T) {
	f := newOAuthFx(t, nil, 0)
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		w := f.do(http.MethodGet, p, "")
		require.Equal(t, 200, w.Code, p)
		m := decode(t, w)
		assert.Equal(t, oauthOrigin+"/mcp", m["resource"])
		assert.Equal(t, []any{oauthOrigin}, m["authorization_servers"])
		assert.Equal(t, []any{"header"}, m["bearer_methods_supported"])
		assert.Equal(t, "KoreGO", m["resource_name"])
	}
	w := f.do(http.MethodGet, "/.well-known/oauth-authorization-server", "")
	require.Equal(t, 200, w.Code)
	m := decode(t, w)
	assert.Equal(t, oauthOrigin, m["issuer"])
	assert.Equal(t, oauthOrigin+"/oauth/authorize", m["authorization_endpoint"])
	assert.Equal(t, oauthOrigin+"/oauth/token", m["token_endpoint"])
	assert.Equal(t, oauthOrigin+"/oauth/register", m["registration_endpoint"])
	assert.Equal(t, []any{"code"}, m["response_types_supported"])
	assert.Equal(t, []any{"authorization_code", "refresh_token"}, m["grant_types_supported"])
	assert.Equal(t, []any{"S256"}, m["code_challenge_methods_supported"])
	assert.Equal(t, []any{"none"}, m["token_endpoint_auth_methods_supported"])
	assert.Equal(t, true, m["authorization_response_iss_parameter_supported"])
}

func TestOAuthMetadata_NoOrigin404(t *testing.T) {
	f := newOAuthFx(t, func(context.Context) (string, bool) { return "", false }, 0)
	for _, c := range [][2]string{
		{http.MethodGet, "/.well-known/oauth-protected-resource"},
		{http.MethodGet, "/.well-known/oauth-protected-resource/mcp"},
		{http.MethodGet, "/.well-known/oauth-authorization-server"},
		{http.MethodPost, "/oauth/register"},
	} {
		w := f.do(c[0], c[1], okReg)
		assert.Equal(t, 404, w.Code, c[1])
		assert.Contains(t, w.Body.String(), "NOT_FOUND", c[1])
	}
}

func TestOAuthMetadata_RequiresHTTPS(t *testing.T) {
	f := newOAuthFx(t, nil, 0)
	r := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	r.RemoteAddr = "203.0.113.5:1"
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	assert.Equal(t, 403, w.Code)
	assert.Contains(t, w.Body.String(), "TOKEN_REQUIRES_HTTPS")
}

func TestOAuthRegister_TolerantVSCodeLike(t *testing.T) {
	f := newOAuthFx(t, nil, 0)
	w := f.register(`{"client_name":"Visual Studio Code","redirect_uris":["https://vscode.dev/redirect","http://127.0.0.1:33418/"],
		"grant_types":["authorization_code","refresh_token","urn:ietf:params:oauth:grant-type:device_code"],
		"token_endpoint_auth_method":"client_secret_basic","scope":"x","client_uri":"https://code.visualstudio.com","extra":{"a":1}}`)
	require.Equal(t, 201, w.Code, w.Body.String())
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	m := decode(t, w)
	assert.Len(t, m["client_id"], 43)
	assert.Equal(t, "Visual Studio Code", m["client_name"])
	assert.Equal(t, []any{"authorization_code", "refresh_token"}, m["grant_types"])
	assert.Equal(t, []any{"code"}, m["response_types"])
	assert.Equal(t, "none", m["token_endpoint_auth_method"])
	assert.Len(t, m["redirect_uris"], 2)
	assert.NotZero(t, m["client_id_issued_at"])
	c, err := f.e.svc.DB.OAuthClientByID(context.Background(), m["client_id"].(string))
	require.NoError(t, err)
	assert.Equal(t, "Visual Studio Code", c.Name)
}

func TestOAuthRegister_RejectsEditorScheme(t *testing.T) {
	f := newOAuthFx(t, nil, 0)
	w := f.register(`{"client_name":"x","redirect_uris":["vscode://x/cb"]}`)
	require.Equal(t, 400, w.Code)
	assert.Equal(t, "invalid_redirect_uri", decode(t, w)["error"])
	w = f.register(`{"client_name":"x"}`)
	assert.Equal(t, 400, w.Code)
	assert.Equal(t, "invalid_redirect_uri", decode(t, w)["error"])
	w = f.register(`não é json`)
	assert.Equal(t, 400, w.Code)
	assert.Equal(t, "invalid_client_metadata", decode(t, w)["error"])
}

func TestOAuthRegister_BodyLimit(t *testing.T) {
	f := newOAuthFx(t, nil, 0)
	w := f.register(`{"client_name":"` + strings.Repeat("a", 70<<10) + `","redirect_uris":["http://127.0.0.1/cb"]}`)
	assert.Equal(t, 400, w.Code)
	assert.Equal(t, "invalid_client_metadata", decode(t, w)["error"])
}

func TestOAuthRegister_RateLimit(t *testing.T) {
	f := newOAuthFx(t, nil, 0)
	for i := 0; i < 10; i++ {
		require.Equal(t, 201, f.register(okReg).Code, i)
	}
	w := f.register(okReg)
	require.Equal(t, 429, w.Code)
	assert.Equal(t, "slow_down", decode(t, w)["error"])
	// Outro IP passa.
	assert.Equal(t, 201, f.register(okReg, withRemote("198.51.100.9:1")).Code)
	// Outro endereço do mesmo /64 IPv6 divide o limite.
	for i := 0; i < 10; i++ {
		f.register(okReg, withRemote("[2001:db8::1:2]:1"))
	}
	assert.Equal(t, 429, f.register(okReg, withRemote("[2001:db8::9]:1")).Code)
}

func TestOAuthRegister_EvictsOldest(t *testing.T) {
	f := newOAuthFx(t, nil, 3)
	var ids []string
	for i := 0; i < 4; i++ {
		w := f.register(okReg, withRemote("198.51.100."+string(rune('1'+i))+":1"))
		require.Equal(t, 201, w.Code)
		ids = append(ids, decode(t, w)["client_id"].(string))
	}
	ctx := context.Background()
	n, err := f.e.svc.DB.CountUnauthorizedOAuthClients(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	_, err = f.e.svc.DB.OAuthClientByID(ctx, ids[0])
	assert.Error(t, err)
	_, err = f.e.svc.DB.OAuthClientByID(ctx, ids[3])
	assert.NoError(t, err)
}

func TestOAuthRoutes_NoCORS(t *testing.T) {
	f := newOAuthFx(t, nil, 0)
	for _, p := range []string{"/oauth/register", "/.well-known/oauth-authorization-server"} {
		w := f.do(http.MethodOptions, p, "", withHeader("Origin", "https://evil.example"),
			withHeader("Access-Control-Request-Method", "POST"))
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"), p)
	}
	w := f.do(http.MethodGet, "/.well-known/oauth-authorization-server", "", withHeader("Origin", "https://evil.example"))
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
}
