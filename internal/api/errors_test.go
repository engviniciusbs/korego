package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/stretchr/testify/assert"
)

func TestWriteError_Format(t *testing.T) {
	w := httptest.NewRecorder()
	api.WriteError(w, http.StatusBadRequest, "X", "m")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"error":{"code":"X","message":"m"}}`, w.Body.String())
}
