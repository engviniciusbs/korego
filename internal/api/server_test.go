package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/node"
	"github.com/engviniciusbs/korego/internal/node/nodetest"
	"github.com/stretchr/testify/assert"
)

func TestHealth(t *testing.T) {
	h := api.NewRouter(api.Deps{})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"status":"ok","version":"dev"}`, w.Body.String())
}

func TestUnknownAPIRoute(t *testing.T) {
	h := api.NewRouter(api.Deps{})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/nada", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.JSONEq(t, `{"error":{"code":"NOT_FOUND","message":"Recurso não encontrado."}}`, w.Body.String())
}

func TestRouter_CoreExcludesSessionOnly(t *testing.T) {
	h := api.CoreForTest(api.Deps{Node: &nodetest.Fake{InfoFn: func(context.Context) (node.HostInfo, error) {
		return node.HostInfo{Hostname: "vps"}, nil
	}}})
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/auth/me"},
		{http.MethodGet, "/auth/sessions"},
		{http.MethodPost, "/auth/logout"},
		{http.MethodGet, "/ws"},
		{http.MethodGet, "/tokens"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(c.method, c.path, nil))
		assert.Equal(t, http.StatusNotFound, w.Code, c.method+" "+c.path)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/system", nil))
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}
