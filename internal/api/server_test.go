package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/engviniciusbs/korego/internal/api"
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
