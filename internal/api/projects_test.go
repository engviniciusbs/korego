package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/store"
)

func TestProjectsAPI_CRUD(t *testing.T) {
	f := newAppsFx(t)
	assert.Equal(t, http.StatusUnauthorized, f.do(t, http.MethodGet, "/api/projects", nil).Code)

	code, b := f.call(t, http.MethodPost, "/api/projects", map[string]any{"name": "Meu site", "description": "d"})
	require.Equal(t, http.StatusCreated, code, b)
	p := b["project"].(map[string]any)
	assert.Equal(t, "Meu site", p["name"])
	id := itoa(int64(p["id"].(float64)))

	code, b = f.call(t, http.MethodPost, "/api/projects", map[string]any{"name": "Meu site"})
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "PROJECT_EXISTS", errCode(b))

	code, b = f.call(t, http.MethodPost, "/api/projects", map[string]any{"name": "  "})
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "INVALID_PROJECT", errCode(b))
	assert.Equal(t, "name", b["error"].(map[string]any)["field"])

	code, b = f.call(t, http.MethodPut, "/api/projects/"+id, map[string]any{"name": "Novo", "description": "x"})
	require.Equal(t, http.StatusOK, code, b)
	assert.Equal(t, "Novo", b["project"].(map[string]any)["name"])

	code, b = f.call(t, http.MethodPut, "/api/projects/999", map[string]any{"name": "Z"})
	require.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "PROJECT_NOT_FOUND", errCode(b))

	code, b = f.call(t, http.MethodGet, "/api/projects", nil)
	require.Equal(t, http.StatusOK, code)
	list := b["projects"].([]any)
	require.Len(t, list, 1)
	assert.EqualValues(t, 0, list[0].(map[string]any)["appCount"])

	code, b = f.call(t, http.MethodDelete, "/api/projects/abc", nil)
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "INVALID_PROJECT_ID", errCode(b))
	code, _ = f.call(t, http.MethodDelete, "/api/projects/"+id, nil)
	assert.Equal(t, http.StatusNoContent, code)
	code, b = f.call(t, http.MethodDelete, "/api/projects/"+id, nil)
	require.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "PROJECT_NOT_FOUND", errCode(b))
}

func TestProjectsAPI_NotEmpty409(t *testing.T) {
	f := newAppsFx(t)
	pid := f.project(t)
	code, _ := f.call(t, http.MethodPost, "/api/apps", imageApp(pid, "web"))
	require.Equal(t, http.StatusCreated, code)

	_, b := f.call(t, http.MethodGet, "/api/projects", nil)
	assert.EqualValues(t, 1, b["projects"].([]any)[0].(map[string]any)["appCount"])

	code, b = f.call(t, http.MethodDelete, "/api/projects/"+itoa(pid), nil)
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "PROJECT_NOT_EMPTY", errCode(b))

	// Filtro por projeto.
	_, b = f.call(t, http.MethodGet, "/api/apps?project="+itoa(pid), nil)
	assert.Len(t, b["apps"].([]any), 1)
	_, b = f.call(t, http.MethodGet, "/api/apps?project=999", nil)
	assert.Len(t, b["apps"].([]any), 0)
	code, b = f.call(t, http.MethodGet, "/api/apps?project=x", nil)
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "INVALID_PROJECT_ID", errCode(b))
}

var _ = context.Background
var _ store.Project
