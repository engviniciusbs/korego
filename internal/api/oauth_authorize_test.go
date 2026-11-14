package api_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
)

// authzFx é o oauthFx com um administrador logado e um cliente registrado.
type authzFx struct {
	*oauthFx
	secret   string
	cookie   *http.Cookie
	csrf     string
	clientID string
}

func newAuthzFx(t *testing.T) *authzFx {
	t.Helper()
	f := &authzFx{oauthFx: newOAuthFx(t, nil, 0)}
	f.secret, f.cookie, f.csrf = f.e.setup(t)
	f.clientID = f.registerClient(t, `{"client_name":"Agente <b>X</b>","redirect_uris":["http://localhost:1/cb","https://vscode.dev/redirect"]}`)
	return f
}

func (f *authzFx) registerClient(t *testing.T, body string) string {
	t.Helper()
	w := f.register(body)
	require.Equal(t, 201, w.Code, w.Body.String())
	return decode(t, w)["client_id"].(string)
}

func (f *authzFx) query(redirect string, extra map[string]string) string {
	q := url.Values{"response_type": {"code"}, "client_id": {f.clientID}, "redirect_uri": {redirect},
		"code_challenge": {"abc"}, "code_challenge_method": {"S256"}, "state": {"estado-1"}}
	for k, v := range extra {
		if v == "" {
			q.Del(k)
		} else {
			q.Set(k, v)
		}
	}
	return "/oauth/authorize?" + q.Encode()
}

// authorize devolve o id do pedido pendente criado.
func (f *authzFx) authorize(t *testing.T, redirect string) string {
	t.Helper()
	w := f.do(http.MethodGet, f.query(redirect, nil), "")
	require.Equal(t, http.StatusFound, w.Code, w.Body.String())
	loc := w.Header().Get("Location")
	require.True(t, strings.HasPrefix(loc, "/autorizar/?req="), loc)
	id := strings.TrimPrefix(loc, "/autorizar/?req=")
	require.Len(t, id, 43)
	return id
}

func (f *authzFx) sess(method, path, body string) *httptest.ResponseRecorder {
	return f.do(method, path, body, withCookie(f.cookie), withHeader("X-CSRF-Token", f.csrf),
		withHeader("Content-Type", "application/json"))
}

func (f *authzFx) fresh(t *testing.T) string {
	*f.e.now = f.e.now.Add(30 * time.Second)
	return f.e.code(t, f.secret)
}

func location(t *testing.T, w *httptest.ResponseRecorder) *url.URL {
	t.Helper()
	u, err := url.Parse(w.Header().Get("Location"))
	require.NoError(t, err)
	return u
}

func TestAuthorize_InvalidClientNoRedirect(t *testing.T) {
	f := newAuthzFx(t)
	w := f.do(http.MethodGet, f.query("http://localhost:1/cb", map[string]string{"client_id": "desconhecido"}), "")
	assert.Equal(t, 400, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	assert.Empty(t, w.Header().Get("Location"))
	assert.Contains(t, w.Body.String(), "Pedido de autorização inválido. Inicie a conexão de novo pelo agente.")
}

func TestAuthorize_RedirectMismatchNoRedirect(t *testing.T) {
	f := newAuthzFx(t)
	for _, red := range []string{"http://localhost.evil.com/cb", "http://localhost:1/outro", "https://vscode.dev/redirect/x", ""} {
		w := f.do(http.MethodGet, f.query(red, nil), "")
		assert.Equal(t, 400, w.Code, red)
		assert.Empty(t, w.Header().Get("Location"), red)
		assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	}
}

func TestAuthorize_MissingPKCERedirectsError(t *testing.T) {
	f := newAuthzFx(t)
	w := f.do(http.MethodGet, f.query("http://localhost:1/cb", map[string]string{"code_challenge": ""}), "")
	require.Equal(t, http.StatusFound, w.Code)
	u := location(t, w)
	assert.Equal(t, "localhost:1", u.Host)
	assert.Equal(t, "invalid_request", u.Query().Get("error"))
	assert.Equal(t, "estado-1", u.Query().Get("state"))
	assert.Equal(t, oauthOrigin, u.Query().Get("iss"))
}

func TestAuthorize_OtherParamErrors(t *testing.T) {
	f := newAuthzFx(t)
	cases := map[string]map[string]string{
		"unsupported_response_type": {"response_type": "token"},
		"invalid_request":           {"code_challenge_method": "plain"},
	}
	for want, extra := range cases {
		w := f.do(http.MethodGet, f.query("http://localhost:1/cb", extra), "")
		require.Equal(t, http.StatusFound, w.Code)
		assert.Equal(t, want, location(t, w).Query().Get("error"))
	}
	w := f.do(http.MethodGet, f.query("http://localhost:1/cb", map[string]string{"state": ""}), "")
	require.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "invalid_request", location(t, w).Query().Get("error"))
}

func TestAuthorize_WrongResource(t *testing.T) {
	f := newAuthzFx(t)
	w := f.do(http.MethodGet, f.query("http://localhost:1/cb", map[string]string{"resource": "https://outro/mcp"}), "")
	require.Equal(t, http.StatusFound, w.Code)
	u := location(t, w)
	assert.Equal(t, "invalid_target", u.Query().Get("error"))
	assert.Equal(t, "estado-1", u.Query().Get("state"))
	// o canônico (normalizado) passa
	w = f.do(http.MethodGet, f.query("http://localhost:1/cb", map[string]string{"resource": "HTTPS://P.exemplo.com:443/mcp/"}), "")
	require.Equal(t, http.StatusFound, w.Code)
	assert.True(t, strings.HasPrefix(w.Header().Get("Location"), "/autorizar/?req="))
}

func TestAuthorize_LoopbackPortVaries(t *testing.T) {
	f := newAuthzFx(t)
	w := f.do(http.MethodGet, f.query("http://localhost:5555/cb", nil), "")
	require.Equal(t, http.StatusFound, w.Code)
	assert.True(t, strings.HasPrefix(w.Header().Get("Location"), "/autorizar/?req="))
}

func TestAuthorize_RateLimit(t *testing.T) {
	f := newAuthzFx(t)
	var last *httptest.ResponseRecorder
	for i := 0; i < 32; i++ {
		last = f.do(http.MethodGet, f.query("http://localhost:1/cb", nil), "")
	}
	assert.Equal(t, 429, last.Code)
	assert.Contains(t, last.Header().Get("Content-Type"), "text/html")
}

func TestOAuthRequest_GetApproveDeny(t *testing.T) {
	f := newAuthzFx(t)
	id := f.authorize(t, "http://localhost:5555/cb")

	w := f.sess(http.MethodGet, "/api/oauth/requests/"+id, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	m := decode(t, w)
	assert.Equal(t, "este computador (localhost:5555)", m["destination"])
	assert.Equal(t, "Agente <b>X</b>", m["clientName"])
	assert.NotEmpty(t, m["registeredAt"])

	// sem sessão: 401
	assert.Equal(t, 401, f.do(http.MethodGet, "/api/oauth/requests/"+id, "").Code)

	w = f.sess(http.MethodPost, "/api/oauth/requests/"+id+"/approve", `{"code":"000000"}`)
	require.Equal(t, 403, w.Code)
	assert.Equal(t, "INVALID_CODE", errCode(decode(t, w)))

	w = f.sess(http.MethodPost, "/api/oauth/requests/"+id+"/approve", `{"code":"`+f.fresh(t)+`"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	u, err := url.Parse(decode(t, w)["redirect"].(string))
	require.NoError(t, err)
	assert.Equal(t, "localhost:5555", u.Host)
	assert.Equal(t, "/cb", u.Path)
	assert.Len(t, u.Query().Get("code"), 43)
	assert.Equal(t, "estado-1", u.Query().Get("state"))
	assert.Equal(t, oauthOrigin, u.Query().Get("iss"))

	c, err := f.e.svc.DB.OAuthClientByID(t.Context(), f.clientID)
	require.NoError(t, err)
	assert.NotNil(t, c.AuthorizedAt)

	w = f.sess(http.MethodPost, "/api/oauth/requests/"+id+"/approve", `{"code":"`+f.fresh(t)+`"}`)
	assert.Equal(t, 404, w.Code)
	assert.Equal(t, "OAUTH_REQUEST_NOT_FOUND", errCode(decode(t, w)))

	au := audits(t, f.e, f.cookie, "token.oauth_authorized")
	var okMeta map[string]any
	for _, a := range au {
		if a["clientId"] != nil {
			okMeta = a
		}
	}
	require.NotNil(t, okMeta)
	assert.Equal(t, "Agente <b>X</b>", okMeta["client"])
	assert.Equal(t, "este computador (localhost:5555)", okMeta["destination"])
	assert.EqualValues(t, 90, okMeta["ttlDays"])

	id2 := f.authorize(t, "http://localhost:5555/cb")
	w = f.sess(http.MethodPost, "/api/oauth/requests/"+id2+"/deny", ``)
	require.Equal(t, 200, w.Code)
	u, _ = url.Parse(decode(t, w)["redirect"].(string))
	assert.Equal(t, "access_denied", u.Query().Get("error"))
	assert.Equal(t, "estado-1", u.Query().Get("state"))
	assert.Equal(t, oauthOrigin, u.Query().Get("iss"))
	assert.Len(t, audits(t, f.e, f.cookie, "token.oauth_denied"), 1)
	assert.Equal(t, 404, f.sess(http.MethodGet, "/api/oauth/requests/"+id2, "").Code)
}

func TestOAuthRequest_SameCodeAsLogin(t *testing.T) {
	f := newAuthzFx(t)
	id := f.authorize(t, "http://localhost:5555/cb")
	w := f.sess(http.MethodGet, "/api/oauth/requests/"+id, "")
	require.Equal(t, 200, w.Code)
	assert.Equal(t, true, decode(t, w)["sessionFresh"])
	// o mesmo instante, portanto o mesmo código que o setup já gastou
	w = f.sess(http.MethodPost, "/api/oauth/requests/"+id+"/approve", `{"code":"`+f.e.code(t, f.secret)+`"}`)
	require.Equal(t, 403, w.Code)
	assert.Equal(t, "INVALID_CODE", errCode(decode(t, w)))

	*f.e.now = f.e.now.Add(3 * time.Minute)
	w = f.sess(http.MethodGet, "/api/oauth/requests/"+id, "")
	require.Equal(t, 200, w.Code)
	assert.Equal(t, false, decode(t, w)["sessionFresh"])
}

func TestOAuthRequest_TokenCannotApprove(t *testing.T) {
	f := newAuthzFx(t)
	id := f.authorize(t, "http://localhost:5555/cb")
	secret, tok, err := f.e.deps.Tokens.Issue(t.Context(), 1, "t", 24*time.Hour)
	require.NoError(t, err)
	// pelo despacho do MCP: as rotas não existem no núcleo (404)
	u, err := f.e.svc.DB.UserByEmail(t.Context(), email)
	require.NoError(t, err)
	d := api.NewDispatcherForTest(f.e.deps)
	for _, c := range [][2]string{
		{http.MethodGet, "/api/oauth/requests/" + id},
		{http.MethodPost, "/api/oauth/requests/" + id + "/approve"},
		{http.MethodPost, "/api/oauth/requests/" + id + "/deny"},
	} {
		st, _ := d.Do(t.Context(), tok, u, agentAddr, http.Header{}, c[0], c[1], nil, map[string]any{"code": f.fresh(t)}, f.fresh(t))
		assert.Equal(t, http.StatusNotFound, st, c[1])
	}
	assert.Equal(t, 200, f.sess(http.MethodGet, "/api/oauth/requests/"+id, "").Code)
	bt := withHeader("Authorization", "Bearer "+secret)
	w := f.do(http.MethodPost, "/api/oauth/requests/"+id+"/approve", `{"code":"`+f.fresh(t)+`"}`, bt)
	assert.Equal(t, 401, w.Code)
	w = f.do(http.MethodGet, "/api/oauth/requests/"+id, "", bt)
	assert.Equal(t, 401, w.Code)
	w = f.do(http.MethodPost, "/api/oauth/requests/"+id+"/deny", ``, bt)
	assert.Equal(t, 401, w.Code)
	// o pedido continua lá
	assert.Equal(t, 200, f.sess(http.MethodGet, "/api/oauth/requests/"+id, "").Code)
	// sem CSRF a sessão também não aprova
	w = f.do(http.MethodPost, "/api/oauth/requests/"+id+"/approve", `{"code":"`+f.fresh(t)+`"}`, withCookie(f.cookie))
	assert.Equal(t, 403, w.Code)
}

func TestOAuthRequest_TTLValidation(t *testing.T) {
	f := newAuthzFx(t)
	id := f.authorize(t, "http://localhost:5555/cb")
	w := f.sess(http.MethodPost, "/api/oauth/requests/"+id+"/approve", `{"code":"`+f.fresh(t)+`","ttlDays":7}`)
	require.Equal(t, 400, w.Code)
	assert.Equal(t, "VALIDATION_ERROR", errCode(decode(t, w)))
	assert.Contains(t, w.Body.String(), "ttlDays")
	// o código não foi gasto e 30 dias é aceito
	w = f.sess(http.MethodPost, "/api/oauth/requests/"+id+"/approve", `{"code":"`+f.fresh(t)+`","ttlDays":30}`)
	assert.Equal(t, 200, w.Code, w.Body.String())
}

func TestPendingStore_CapAndTTL(t *testing.T) {
	p := api.NewPendingStoreForTest()
	t0 := time.Unix(1_800_000_000, 0)
	for i := 0; i < 257; i++ {
		p.Add("id"+strconv.Itoa(i), t0.Add(time.Duration(i)*time.Millisecond))
	}
	now := t0.Add(time.Second)
	assert.False(t, p.Has("id0", now), "o mais antigo sai no 257º")
	assert.True(t, p.Has("id1", now))
	assert.True(t, p.Has("id256", now))
	assert.False(t, p.Has("id256", t0.Add(11*time.Minute)), "aos 11 min sumiu")
}
