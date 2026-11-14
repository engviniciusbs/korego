package api_test

import (
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

type tokFx struct {
	e      *env
	secret string
	cookie *http.Cookie
	csrf   string
	plain  bool // sem TLS (o padrão é HTTPS)
}

func newTokFx(t *testing.T) *tokFx {
	t.Helper()
	tk := &auth.Tokens{}
	e := newEnvDeps(t, api.Deps{Tokens: tk})
	tk.DB, tk.Now = e.svc.DB, e.svc.Now
	e.deps.TokenLimiter = nil
	secret, c, csrf := e.setup(t)
	e.totp = secret
	return &tokFx{e: e, secret: secret, cookie: c, csrf: csrf}
}

// req faz a chamada do painel (cookie + CSRF).
func (f *tokFx) req(t *testing.T, method, path string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	t.Helper()
	o := []reqOpt{withCookie(f.cookie), withHeader("X-CSRF-Token", f.csrf)}
	if !f.plain {
		o = append(o, withTLS())
	}
	o = append(o, opts...)
	return f.e.do(t, method, path, body, o...)
}

// fresh avança o relógio para um código TOTP ainda não usado.
func (f *tokFx) fresh(t *testing.T) string {
	*f.e.now = f.e.now.Add(30 * time.Second)
	return f.e.code(t, f.secret)
}

func (f *tokFx) create(t *testing.T, name string, days int, code string, opts ...reqOpt) *httptest.ResponseRecorder {
	return f.req(t, http.MethodPost, "/api/tokens",
		map[string]any{"name": name, "expiresInDays": days, "code": code}, opts...)
}

func TestTokens_CreateListRevoke(t *testing.T) {
	f := newTokFx(t)
	w := f.create(t, "claude-code", 90, f.fresh(t))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	out := decode(t, w)
	secret, _ := out["token"].(string)
	require.Contains(t, secret, "kg_")
	item := out["item"].(map[string]any)
	assert.Equal(t, "claude-code", item["name"])
	assert.NotNil(t, item["expiresAt"])
	id := int(item["id"].(float64))

	w = f.req(t, http.MethodGet, "/api/tokens", nil)
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), secret)
	assert.NotContains(t, w.Body.String(), "tokenHash")
	assert.NotContains(t, w.Body.String(), "token_hash")
	items := decode(t, w)["items"].([]any)
	require.Len(t, items, 1)
	li := items[0].(map[string]any)
	for _, k := range []string{"id", "name", "prefix", "createdAt", "lastUsedAt", "lastUsedIp", "expiresAt", "kind"} {
		assert.Contains(t, li, k)
	}
	assert.Len(t, li, 8)
	assert.Equal(t, "manual", li["kind"])

	w = f.req(t, http.MethodDelete, "/api/tokens/"+itoa(int64(id)), nil)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	w = f.req(t, http.MethodGet, "/api/tokens", nil)
	assert.Empty(t, decode(t, w)["items"])

	cr := audits(t, f.e, f.cookie, "api_token.create")
	require.Len(t, cr, 1)
	assert.Equal(t, "claude-code", cr[0]["name"])
	rv := audits(t, f.e, f.cookie, "api_token.revoke")
	require.Len(t, rv, 1)
	assert.Equal(t, "claude-code", rv[0]["name"])
	for _, m := range append(cr, rv...) {
		assert.NotContains(t, m, "token")
		assert.NotContains(t, m, "secret")
	}
}

func TestTokens_NeverExpires(t *testing.T) {
	f := newTokFx(t)
	w := f.create(t, "sem-fim", 0, f.fresh(t))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Nil(t, decode(t, w)["item"].(map[string]any)["expiresAt"])
}

func TestTokens_CreateNeedsCode(t *testing.T) {
	f := newTokFx(t)
	w := f.create(t, "a", 30, "")
	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "CODE_REQUIRED", errCode(decode(t, w)))
	w = f.create(t, "a", 30, "000000")
	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "INVALID_CODE", errCode(decode(t, w)))
	// o limitador de login vale: 5 tentativas erradas bloqueiam
	for i := 0; i < 6; i++ {
		w = f.create(t, "a", 30, "000000")
	}
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestTokens_CreateRefusedOverPlainHTTP(t *testing.T) {
	f := newTokFx(t)
	code := f.fresh(t)
	f.plain = true
	w := f.create(t, "a", 30, code, withRemote("203.0.113.5:1"))
	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "TOKEN_REQUIRES_HTTPS", errCode(decode(t, w)))
	assert.Equal(t, tokenHTTPSMsg, decode(t, w)["error"].(map[string]any)["message"])
	// o código não foi consumido
	f.plain = false
	w = f.create(t, "a", 30, code, withRemote("203.0.113.5:1"))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
}

func TestTokens_Validation(t *testing.T) {
	f := newTokFx(t)
	w := f.create(t, "a", 7, f.fresh(t))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "VALIDATION_ERROR", errCode(decode(t, w)))
	w = f.create(t, "  ", 30, f.fresh(t))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "VALIDATION_ERROR", errCode(decode(t, w)))
	require.Equal(t, http.StatusCreated, f.create(t, "dup", 30, f.fresh(t)).Code)
	w = f.create(t, "dup", 30, f.fresh(t))
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "TOKEN_NAME_TAKEN", errCode(decode(t, w)))
	w = f.req(t, http.MethodDelete, "/api/tokens/9999", nil)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "TOKEN_NOT_FOUND", errCode(decode(t, w)))
}

func TestTokens_NotReachableByToken(t *testing.T) {
	f := newTokFx(t)
	ctx := context.Background()
	u, err := f.e.svc.DB.UserByEmail(ctx, email)
	require.NoError(t, err)
	_, tok, err := f.e.deps.Tokens.Issue(ctx, u.ID, "agente", 0)
	require.NoError(t, err)
	d := api.NewDispatcherForTest(f.e.deps)
	st, _ := d.Do(ctx, tok, u, agentAddr, http.Header{}, http.MethodGet, "/api/tokens", nil, nil, "")
	assert.Equal(t, http.StatusNotFound, st)
}

func TestTokens_RejectedNameDoesNotBurnCode(t *testing.T) {
	f := newTokFx(t)
	require.Equal(t, http.StatusCreated, f.create(t, "dup", 30, f.fresh(t)).Code)

	code := f.fresh(t)
	w := f.create(t, "dup", 30, code)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "TOKEN_NAME_TAKEN", errCode(decode(t, w)))

	// o mesmo código segue válido: nada o consumiu
	w = f.create(t, "outro", 30, code)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	code = f.fresh(t)
	w = f.create(t, "   ", 30, code)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "VALIDATION_ERROR", errCode(decode(t, w)))
	w = f.create(t, strings.Repeat("x", 65), 30, code)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Equal(t, http.StatusCreated, f.create(t, "terceiro", 30, code).Code)
}

func TestTokens_MissingExpiryDefaultsTo90Days(t *testing.T) {
	f := newTokFx(t)
	w := f.req(t, http.MethodPost, "/api/tokens", map[string]any{"name": "sem-campo", "code": f.fresh(t)})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	exp, _ := decode(t, w)["item"].(map[string]any)["expiresAt"].(string)
	require.NotEmpty(t, exp)
	at, err := time.Parse(time.RFC3339, exp)
	require.NoError(t, err)
	assert.WithinDuration(t, f.e.now.Add(90*24*time.Hour), at, time.Minute)
}
