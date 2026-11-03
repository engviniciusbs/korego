package api_test

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/audit"
	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/secrets"
	"github.com/engviniciusbs/korego/internal/store"
)

const (
	email    = "admin@exemplo.com"
	password = "senha-forte-de-teste"
	remoteIP = "203.0.113.10"
)

type env struct {
	h   http.Handler
	now *time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := store.OpenMemory(t)
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	box, err := secrets.NewBox(key)
	require.NoError(t, err)
	now := time.Unix(1_800_000_015, 0)
	clock := func() time.Time { return now }
	svc := &auth.Service{
		DB:       db,
		Box:      box,
		Sessions: &auth.Sessions{DB: db, Now: clock},
		Limiter:  auth.NewLimiter(5, time.Minute, 15*time.Minute),
		Audit:    &audit.Logger{DB: db, Now: clock},
		Now:      clock,
	}
	return &env{h: api.NewRouter(api.Deps{Auth: svc}), now: &now}
}

type reqOpt func(*http.Request)

func withCookie(c *http.Cookie) reqOpt { return func(r *http.Request) { r.AddCookie(c) } }
func withHeader(k, v string) reqOpt    { return func(r *http.Request) { r.Header.Set(k, v) } }
func withRemote(a string) reqOpt       { return func(r *http.Request) { r.RemoteAddr = a } }

func (e *env) do(t *testing.T, method, path string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rd)
	r.RemoteAddr = remoteIP + ":5555"
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func (e *env) code(t *testing.T, secret string) string {
	t.Helper()
	c, err := totp.GenerateCodeCustom(secret, *e.now,
		totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	require.NoError(t, err)
	return c
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m), w.Body.String())
	return m
}

func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == "korego_session" {
			return c
		}
	}
	t.Fatalf("cookie korego_session ausente: %v", w.Header())
	return nil
}

// setup runs the setup flow and returns the TOTP secret, cookie and CSRF token.
func (e *env) setup(t *testing.T) (string, *http.Cookie, string) {
	t.Helper()
	w := e.do(t, http.MethodPost, "/api/setup", map[string]string{"email": email, "password": password})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	ch := decode(t, w)
	secret := ch["secret"].(string)
	require.Contains(t, ch["otpauthUrl"], "otpauth://totp/")

	w = e.do(t, http.MethodPost, "/api/setup/confirm", map[string]string{"token": ch["token"].(string), "code": e.code(t, secret)})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decode(t, w)
	require.Len(t, body["recoveryCodes"], 10)
	return secret, sessionCookie(t, w), body["csrfToken"].(string)
}

func (e *env) login(t *testing.T, secret string, opts ...reqOpt) *httptest.ResponseRecorder {
	t.Helper()
	*e.now = e.now.Add(30 * time.Second)
	return e.do(t, http.MethodPost, "/api/auth/login",
		map[string]string{"email": email, "password": password, "code": e.code(t, secret)}, opts...)
}

func TestSetupFlow_HappyPath(t *testing.T) {
	e := newEnv(t)
	w := e.do(t, http.MethodGet, "/api/setup/status", nil)
	assert.JSONEq(t, `{"needsSetup":true}`, w.Body.String())

	w = e.do(t, http.MethodPost, "/api/setup", map[string]string{"email": email, "password": password})
	require.Equal(t, http.StatusOK, w.Code)
	ch := decode(t, w)
	w = e.do(t, http.MethodPost, "/api/setup/confirm",
		map[string]string{"token": ch["token"].(string), "code": e.code(t, ch["secret"].(string))})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decode(t, w)
	require.Len(t, body["recoveryCodes"], 10)
	require.NotEmpty(t, body["csrfToken"])
	user := body["user"].(map[string]any)
	require.Equal(t, email, user["email"])
	require.Equal(t, "admin", user["role"])
	require.ElementsMatch(t, []string{"id", "email", "role", "createdAt"}, keys(user))
	c := sessionCookie(t, w)
	require.NotEmpty(t, c.Value)

	w = e.do(t, http.MethodGet, "/api/auth/me", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, body["csrfToken"], decode(t, w)["csrfToken"])

	w = e.do(t, http.MethodGet, "/api/setup/status", nil)
	assert.JSONEq(t, `{"needsSetup":false}`, w.Body.String())
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestSetup_Errors(t *testing.T) {
	e := newEnv(t)
	w := e.do(t, http.MethodPost, "/api/setup", map[string]string{"email": email, "password": "curta"})
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), `"WEAK_PASSWORD"`)

	w = e.do(t, http.MethodPost, "/api/setup", map[string]string{"email": "Fulano <a@exemplo.com>", "password": password})
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), `"VALIDATION_ERROR"`)

	w = e.do(t, http.MethodPost, "/api/setup", map[string]any{"email": email, "password": password, "role": "x"})
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), `"VALIDATION_ERROR"`)

	w = e.do(t, http.MethodPost, "/api/setup/confirm", map[string]string{"token": "nada", "code": "123456"})
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), `"SETUP_TOKEN_INVALID"`)

	w = e.do(t, http.MethodPost, "/api/setup", map[string]string{"email": email, "password": password})
	tok := decode(t, w)["token"].(string)
	w = e.do(t, http.MethodPost, "/api/setup/confirm", map[string]string{"token": tok, "code": "abc"})
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), `"INVALID_TOTP"`)
}

func TestSetup_AfterDone_409(t *testing.T) {
	e := newEnv(t)
	e.setup(t)
	w := e.do(t, http.MethodPost, "/api/setup", map[string]string{"email": "outro@exemplo.com", "password": password})
	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, w.Body.String(), `"SETUP_ALREADY_DONE"`)
}

func TestLogin_TOTPReplayRejected(t *testing.T) {
	e := newEnv(t)
	secret, _, _ := e.setup(t)
	w := e.login(t, secret)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	w = e.do(t, http.MethodPost, "/api/auth/login",
		map[string]string{"email": email, "password": password, "code": e.code(t, secret)})
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Body.String(), `"INVALID_CREDENTIALS"`)
}

func TestLogin_GenericError(t *testing.T) {
	e := newEnv(t)
	secret, _, _ := e.setup(t)
	*e.now = e.now.Add(30 * time.Second)
	good := e.code(t, secret)
	cases := []map[string]string{
		{"email": "ninguem@exemplo.com", "password": password, "code": good},
		{"email": email, "password": "senha-errada-123", "code": good},
		{"email": email, "password": password, "code": "000000"},
	}
	var bodies []string
	for _, c := range cases {
		w := e.do(t, http.MethodPost, "/api/auth/login", c)
		require.Equal(t, http.StatusUnauthorized, w.Code)
		bodies = append(bodies, w.Body.String())
	}
	require.JSONEq(t, `{"error":{"code":"INVALID_CREDENTIALS","message":"E-mail, senha ou código inválidos."}}`, bodies[0])
	require.Equal(t, bodies[0], bodies[1])
	require.Equal(t, bodies[0], bodies[2])
}

func TestLogin_RateLimited(t *testing.T) {
	e := newEnv(t)
	secret, _, _ := e.setup(t)
	for range 5 {
		w := e.do(t, http.MethodPost, "/api/auth/login",
			map[string]string{"email": email, "password": "senha-errada-123", "code": "000000"})
		require.Equal(t, http.StatusUnauthorized, w.Code)
	}
	w := e.login(t, secret)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Contains(t, w.Body.String(), `"RATE_LIMITED"`)
}

func TestCSRF_Required(t *testing.T) {
	e := newEnv(t)
	_, c, csrf := e.setup(t)
	w := e.do(t, http.MethodPost, "/api/auth/logout", nil, withCookie(c))
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), `"CSRF_INVALID"`)

	w = e.do(t, http.MethodPost, "/api/auth/logout", nil, withCookie(c), withHeader("X-CSRF-Token", csrf+"x"))
	require.Equal(t, http.StatusForbidden, w.Code)

	w = e.do(t, http.MethodPost, "/api/auth/logout", nil, withCookie(c), withHeader("X-CSRF-Token", csrf))
	require.Equal(t, http.StatusNoContent, w.Code)
	cleared := sessionCookie(t, w)
	require.Empty(t, cleared.Value)
	require.Equal(t, -1, cleared.MaxAge)

	w = e.do(t, http.MethodGet, "/api/auth/me", nil, withCookie(c))
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestCookieAttributes(t *testing.T) {
	e := newEnv(t)
	secret, c, _ := e.setup(t)
	require.True(t, c.HttpOnly)
	require.Equal(t, http.SameSiteStrictMode, c.SameSite)
	require.Equal(t, "/", c.Path)
	require.Equal(t, 604800, c.MaxAge)
	require.False(t, c.Secure)

	// X-Forwarded-Proto is ignored from a non-loopback peer.
	w := e.login(t, secret, withHeader("X-Forwarded-Proto", "https"))
	require.Equal(t, http.StatusOK, w.Code)
	require.False(t, sessionCookie(t, w).Secure)

	// A local reverse proxy terminating TLS marks the cookie Secure.
	w = e.login(t, secret, withRemote("127.0.0.1:4000"), withHeader("X-Forwarded-Proto", "https"))
	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, sessionCookie(t, w).Secure)
}

func TestAudit_RecordsLoginAndFailure(t *testing.T) {
	e := newEnv(t)
	secret, _, _ := e.setup(t)
	w := e.do(t, http.MethodPost, "/api/auth/login",
		map[string]string{"email": email, "password": "senha-errada-123", "code": "000000"})
	require.Equal(t, http.StatusUnauthorized, w.Code)
	w = e.login(t, secret)
	require.Equal(t, http.StatusOK, w.Code)
	c := sessionCookie(t, w)

	w = e.do(t, http.MethodGet, "/api/audit", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "senha-errada-123")
	body := decode(t, w)
	require.Nil(t, body["nextBefore"])
	items := body["items"].([]any)
	require.Len(t, items, 3)
	actions := map[string]map[string]any{}
	for _, it := range items {
		m := it.(map[string]any)
		actions[m["action"].(string)] = m
	}
	require.Contains(t, actions, "auth.setup")
	failed := actions["auth.login_failed"]
	require.Equal(t, remoteIP, failed["ip"])
	require.Equal(t, email, failed["metadata"].(map[string]any)["email"])
	require.Equal(t, remoteIP, actions["auth.login"]["ip"])
	require.NotNil(t, actions["auth.login"]["userId"])
	// Newest first.
	require.Equal(t, "auth.login", items[0].(map[string]any)["action"])

	w = e.do(t, http.MethodGet, "/api/audit?limit=2", nil, withCookie(c))
	body = decode(t, w)
	require.Len(t, body["items"], 2)
	next := body["nextBefore"].(float64)
	w = e.do(t, http.MethodGet, "/api/audit?limit=2&before="+jsonNum(next), nil, withCookie(c))
	body = decode(t, w)
	require.Len(t, body["items"], 1)
	require.Equal(t, "auth.setup", body["items"].([]any)[0].(map[string]any)["action"])

	w = e.do(t, http.MethodGet, "/api/audit?limit=abc", nil, withCookie(c))
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func jsonNum(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func TestMe_Unauthorized(t *testing.T) {
	e := newEnv(t)
	w := e.do(t, http.MethodGet, "/api/auth/me", nil)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.JSONEq(t, `{"error":{"code":"UNAUTHORIZED","message":"Sessão inválida ou expirada."}}`, w.Body.String())

	w = e.do(t, http.MethodGet, "/api/auth/me", nil, withCookie(&http.Cookie{Name: "korego_session", Value: "falso"}))
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestSessions_ListAndRevoke(t *testing.T) {
	e := newEnv(t)
	secret, c1, csrf1 := e.setup(t)
	w := e.login(t, secret)
	require.Equal(t, http.StatusOK, w.Code)
	c2 := sessionCookie(t, w)

	w = e.do(t, http.MethodGet, "/api/auth/sessions", nil, withCookie(c1))
	require.Equal(t, http.StatusOK, w.Code)
	items := decode(t, w)["items"].([]any)
	require.Len(t, items, 2)
	var other string
	currents := 0
	for _, it := range items {
		m := it.(map[string]any)
		require.Equal(t, remoteIP, m["ip"])
		if m["current"].(bool) {
			currents++
		} else {
			other = m["id"].(string)
		}
	}
	require.Equal(t, 1, currents)
	require.NotContains(t, w.Body.String(), c1.Value)

	w = e.do(t, http.MethodDelete, "/api/auth/sessions/"+other, nil, withCookie(c1))
	require.Equal(t, http.StatusForbidden, w.Code)
	w = e.do(t, http.MethodDelete, "/api/auth/sessions/"+other, nil, withCookie(c1), withHeader("X-CSRF-Token", csrf1))
	require.Equal(t, http.StatusNoContent, w.Code)
	w = e.do(t, http.MethodDelete, "/api/auth/sessions/"+other, nil, withCookie(c1), withHeader("X-CSRF-Token", csrf1))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), `"NOT_FOUND"`)

	w = e.do(t, http.MethodGet, "/api/auth/me", nil, withCookie(c2))
	require.Equal(t, http.StatusUnauthorized, w.Code)

	w = e.do(t, http.MethodGet, "/api/audit", nil, withCookie(c1))
	require.Contains(t, w.Body.String(), `"auth.session_revoked"`)
	require.Contains(t, w.Body.String(), `"target":"`+other+`"`)
}

func TestMalformedBody(t *testing.T) {
	e := newEnv(t)
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":`))
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), `"VALIDATION_ERROR"`)
}
