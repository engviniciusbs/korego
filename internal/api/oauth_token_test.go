package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
)

const tokRedirect = "http://localhost:1/cb"

// grantFx é o authzFx com helpers para obter um código e chamar o /oauth/token.
type grantFx struct{ *authzFx }

func newGrantFx(t *testing.T) *grantFx { return &grantFx{newAuthzFx(t)} }

func s256(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// verifier é um code_verifier válido (43 caracteres).
const verifier = "verificador-de-teste-com-mais-de-43-caracteres-ok"

// authCode percorre authorize + approve e devolve o código emitido.
func (f *grantFx) authCode(t *testing.T) string {
	t.Helper()
	w := f.do(http.MethodGet, f.query(tokRedirect, map[string]string{"code_challenge": s256(verifier)}), "")
	require.Equal(t, http.StatusFound, w.Code, w.Body.String())
	id := strings.TrimPrefix(w.Header().Get("Location"), "/autorizar/?req=")
	w = f.sess(http.MethodPost, "/api/oauth/requests/"+id+"/approve", `{"code":"`+f.fresh(t)+`"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	u, err := url.Parse(decode(t, w)["redirect"].(string))
	require.NoError(t, err)
	return u.Query().Get("code")
}

func (f *grantFx) token(form url.Values, opts ...reqOpt) *httptest.ResponseRecorder {
	opts = append([]reqOpt{withHeader("Content-Type", "application/x-www-form-urlencoded")}, opts...)
	return f.do(http.MethodPost, "/oauth/token", form.Encode(), opts...)
}

func (f *grantFx) exchange(code string, over map[string]string) *httptest.ResponseRecorder {
	v := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {tokRedirect},
		"client_id": {f.clientID}, "code_verifier": {verifier}}
	for k, x := range over {
		v.Set(k, x)
	}
	return f.token(v)
}

func (f *grantFx) refresh(refresh string) *httptest.ResponseRecorder {
	return f.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {f.clientID}})
}

// mcpStatus faz um tools/list em /mcp com o acesso dado.
func (f *grantFx) mcpStatus(access string) int {
	w := f.do(http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		withHeader("Content-Type", "application/json"), withHeader("Accept", "application/json, text/event-stream"),
		bearer(access))
	return w.Code
}

func TestToken_CodeExchange(t *testing.T) {
	f := newGrantFx(t)
	w := f.exchange(f.authCode(t), map[string]string{"resource": oauthOrigin + "/mcp"})
	require.Equal(t, 200, w.Code, w.Body.String())
	m := decode(t, w)
	assert.Equal(t, "Bearer", m["token_type"])
	assert.EqualValues(t, 3600, m["expires_in"])
	assert.True(t, strings.HasPrefix(m["access_token"].(string), "kg_"))
	assert.True(t, strings.HasPrefix(m["refresh_token"].(string), "kgr_"))
	assert.Equal(t, 200, f.mcpStatus(m["access_token"].(string)))
}

func TestToken_CodeReuseRevokes(t *testing.T) {
	f := newGrantFx(t)
	code := f.authCode(t)
	w := f.exchange(code, nil)
	require.Equal(t, 200, w.Code)
	access := decode(t, w)["access_token"].(string)
	require.Equal(t, 200, f.mcpStatus(access))

	w = f.exchange(code, nil)
	require.Equal(t, 400, w.Code)
	assert.Equal(t, "invalid_grant", decode(t, w)["error"])
	assert.Equal(t, 401, f.mcpStatus(access))
}

func TestToken_InvalidGrants(t *testing.T) {
	cases := map[string]map[string]string{
		"verifier":     {"code_verifier": "outro-verificador-com-mais-de-43-caracteres-aaaa"},
		"redirect":     {"redirect_uri": "http://localhost:2/cb"},
		"client":       {"client_id": "desconhecido"},
		"sem code":     {"code": "nao-existe"},
		"sem verifier": {"code_verifier": ""},
	}
	for name, over := range cases {
		t.Run(name, func(t *testing.T) {
			f := newGrantFx(t)
			w := f.exchange(f.authCode(t), over)
			require.Equal(t, 400, w.Code, w.Body.String())
			assert.Equal(t, "invalid_grant", decode(t, w)["error"])
		})
	}
}

func TestToken_ExpiredCode(t *testing.T) {
	f := newGrantFx(t)
	code := f.authCode(t)
	*f.skew += 61 * time.Second
	w := f.exchange(code, nil)
	require.Equal(t, 400, w.Code)
	assert.Equal(t, "invalid_grant", decode(t, w)["error"])
}

func TestToken_WrongResource(t *testing.T) {
	f := newGrantFx(t)
	w := f.exchange(f.authCode(t), map[string]string{"resource": "https://outro.exemplo.com/mcp"})
	require.Equal(t, 400, w.Code)
	assert.Equal(t, "invalid_target", decode(t, w)["error"])
}

func TestToken_BadRequests(t *testing.T) {
	f := newGrantFx(t)
	w := f.do(http.MethodPost, "/oauth/token", `{"grant_type":"refresh_token"}`, withHeader("Content-Type", "application/json"))
	require.Equal(t, 400, w.Code)
	assert.Equal(t, "invalid_request", decode(t, w)["error"])

	w = f.token(url.Values{"grant_type": {"password"}})
	require.Equal(t, 400, w.Code)
	assert.Equal(t, "unsupported_grant_type", decode(t, w)["error"])
}

func TestToken_Refresh(t *testing.T) {
	f := newGrantFx(t)
	m := decode(t, f.exchange(f.authCode(t), nil))
	r1, a1 := m["refresh_token"].(string), m["access_token"].(string)

	w := f.refresh(r1)
	require.Equal(t, 200, w.Code, w.Body.String())
	m2 := decode(t, w)
	r2, a2 := m2["refresh_token"].(string), m2["access_token"].(string)
	assert.NotEqual(t, r1, r2)
	assert.NotEqual(t, a1, a2)
	assert.Equal(t, 200, f.mcpStatus(a2))

	// dentro da janela de 60 s, o refresh anterior repete o mesmo par
	w = f.refresh(r1)
	require.Equal(t, 200, w.Code)
	assert.Equal(t, r2, decode(t, w)["refresh_token"])

	// fora da janela, é reuso: invalid_grant e revoga a conexão
	*f.e.now = f.e.now.Add(61 * time.Second)
	w = f.refresh(r1)
	require.Equal(t, 400, w.Code)
	assert.Equal(t, "invalid_grant", decode(t, w)["error"])
	assert.Equal(t, 401, f.mcpStatus(a2))
	au := audits(t, f.e, f.cookie, "token.refresh_reuse")
	require.Len(t, au, 1)
	assert.NotEmpty(t, au[0]["client"])
	assert.NotZero(t, au[0]["tokenId"])

	// cliente errado ou refresh desconhecido
	assert.Equal(t, 400, f.refresh("kgr_"+strings.Repeat("A", 43)).Code)
}

func TestToken_NoStore(t *testing.T) {
	f := newGrantFx(t)
	for _, w := range []*httptest.ResponseRecorder{f.exchange(f.authCode(t), nil), f.exchange("x", nil)} {
		assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		assert.Equal(t, "no-cache", w.Header().Get("Pragma"))
	}
}

func TestToken_RateLimitSlowDown(t *testing.T) {
	f := newGrantFx(t)
	for i := 0; i < 10; i++ {
		require.Equal(t, 400, f.exchange("x", nil).Code)
	}
	w := f.exchange(f.authCode(t), nil)
	require.Equal(t, 429, w.Code)
	assert.Equal(t, "slow_down", decode(t, w)["error"])
}

func TestToken_NoOriginStillServesMCP(t *testing.T) {
	has := true
	f := &grantFx{&authzFx{oauthFx: newOAuthFx(t, func(ctx context.Context) (string, bool) { return oauthOrigin, has }, 0)}}
	f.secret, f.cookie, f.csrf = f.e.setup(t)
	f.clientID = f.registerClient(t, `{"client_name":"Agente","redirect_uris":["http://localhost:1/cb"]}`)
	m := decode(t, f.exchange(f.authCode(t), nil))
	has = false
	assert.Equal(t, 404, f.exchange("x", nil).Code)
	assert.Equal(t, 200, f.mcpStatus(m["access_token"].(string)))
}

// Dois usos do código antes de a conexão do primeiro ser registrada: setToken
// reporta o reuso para quem emitiu revogar.
func TestCodeStore_ReplayBeforeSetToken(t *testing.T) {
	cs := api.NewCodeStoreForTest()
	now := time.Now()
	cs.Put("h", now)
	used, ok := cs.Take("h", now)
	require.True(t, ok)
	assert.False(t, used)
	used, ok = cs.Take("h", now)
	require.True(t, ok)
	assert.True(t, used)
	assert.True(t, cs.SetToken("h", 7), "reuso durante a emissão")

	// sem reuso concorrente, setToken não reporta nada
	cs.Put("g", now)
	_, _ = cs.Take("g", now)
	assert.False(t, cs.SetToken("g", 8))
}

func TestToken_RefreshWrongClient(t *testing.T) {
	f := newGrantFx(t)
	m := decode(t, f.exchange(f.authCode(t), nil))
	w := f.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {m["refresh_token"].(string)},
		"client_id": {"outro-cliente"}})
	require.Equal(t, 400, w.Code)
	assert.Equal(t, "invalid_grant", decode(t, w)["error"])
	// a conexão não foi revogada e o cliente certo ainda renova
	assert.Equal(t, 200, f.mcpStatus(m["access_token"].(string)))
	assert.Equal(t, 200, f.refresh(m["refresh_token"].(string)).Code)
}

func TestToken_ClientGoneBeforeExchange(t *testing.T) {
	f := newGrantFx(t)
	u, err := f.e.svc.DB.UserByEmail(t.Context(), email)
	require.NoError(t, err)
	h, put := api.OAuthRouterWithCodesForTest(f.e.deps)
	put("codigo-fantasma", "cliente-sumiu", tokRedirect, s256(verifier), u.ID)
	r := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(url.Values{
		"grant_type": {"authorization_code"}, "code": {"codigo-fantasma"}, "redirect_uri": {tokRedirect},
		"client_id": {"cliente-sumiu"}, "code_verifier": {verifier}}.Encode()))
	r.RemoteAddr = "203.0.113.5:1"
	withTLS()(r)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, 400, w.Code, w.Body.String())
	assert.Equal(t, "invalid_grant", decode(t, w)["error"])
	toks, err := f.e.svc.DB.ListAPITokens(t.Context(), u.ID)
	require.NoError(t, err)
	assert.Empty(t, toks, "nenhuma conexão emitida")
}
