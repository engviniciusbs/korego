package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/webui"
	"github.com/stretchr/testify/assert"
)

func TestSecurityHeaders(t *testing.T) {
	h := api.SecurityHeaders(api.NewRouter(api.Deps{}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'",
		w.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "same-origin", w.Header().Get("Referrer-Policy"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
}

func TestAPIPathNotServedByWeb(t *testing.T) {
	web := webui.NewHandler(fstest.MapFS{"index.html": {Data: []byte("<html>web</html>")}})
	h := api.NewRouter(api.Deps{Web: web})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/nada", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.JSONEq(t, `{"error":{"code":"NOT_FOUND","message":"Recurso não encontrado."}}`, w.Body.String())

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "web")
}

func TestNilWebIs404(t *testing.T) {
	h := api.NewRouter(api.Deps{})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/login/", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}
