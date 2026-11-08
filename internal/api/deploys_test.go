package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/deploy"
	"github.com/engviniciusbs/korego/internal/store"
)

func (f *appsFx) makeApp(t *testing.T, slug string) string {
	t.Helper()
	code, b := f.call(t, http.MethodPost, "/api/apps", imageApp(f.project(t), slug))
	require.Equal(t, http.StatusCreated, code, b)
	return itoa(appID(b))
}

func TestDeploysAPI_CreateListLogCancel(t *testing.T) {
	f := newAppsFx(t)
	aid := f.makeApp(t, "web")

	code, b := f.call(t, http.MethodPost, "/api/apps/"+aid+"/deploys", nil)
	require.Equal(t, http.StatusAccepted, code, b)
	d := b["deploy"].(map[string]any)
	assert.Equal(t, "manual", d["trigger"])
	assert.Contains(t, []any{"queued", "building"}, d["status"])
	assert.Contains(t, d, "commitSha")
	assert.Contains(t, d, "createdAt")
	id64 := int64(d["id"].(float64))
	did := itoa(id64)

	code, b = f.call(t, http.MethodGet, "/api/apps/"+aid+"/deploys?limit=20", nil)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, b["deploys"].([]any), 1)
	code, b = f.call(t, http.MethodGet, "/api/apps/"+aid+"/deploys?limit=0", nil)
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "INVALID_LIMIT", errCode(b))

	code, b = f.call(t, http.MethodGet, "/api/deploys/"+did, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, did, itoa(int64(b["deploy"].(map[string]any)["id"].(float64))))

	// Cancelar o que está rodando (o pull bloqueia até o ctx cancelar).
	code, _ = f.call(t, http.MethodPost, "/api/deploys/"+did+"/cancel", nil)
	require.Equal(t, http.StatusNoContent, code)
	waitFor(t, func() bool {
		m, err := f.db.GetDeploy(context.Background(), id64)
		return err == nil && m.Status == "cancelled"
	})
	waitFor(t, func() bool { return deploy.LogFinished(f.dataDir, id64) })
	_, b = f.call(t, http.MethodGet, "/api/deploys/"+did+"/log?from=0", nil)
	assert.Equal(t, true, b["done"])

	code, b = f.call(t, http.MethodPost, "/api/deploys/"+did+"/cancel", nil)
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "DEPLOY_FINISHED", errCode(b))

	code, b = f.call(t, http.MethodGet, "/api/deploys/999", nil)
	require.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "DEPLOY_NOT_FOUND", errCode(b))
	code, b = f.call(t, http.MethodPost, "/api/deploys/999/cancel", nil)
	require.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "DEPLOY_NOT_FOUND", errCode(b))
	code, b = f.call(t, http.MethodPost, "/api/apps/999/deploys", nil)
	require.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "APP_NOT_FOUND", errCode(b))
	code, b = f.call(t, http.MethodGet, "/api/deploys/x", nil)
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "INVALID_DEPLOY_ID", errCode(b))

	w := f.do(t, http.MethodGet, "/api/audit", nil, withCookie(f.cookie))
	for _, a := range []string{"deploy.create", "deploy.cancel", `"trigger":"manual"`} {
		assert.Contains(t, w.Body.String(), a)
	}
}

func TestDeploysAPI_LogPaging(t *testing.T) {
	f := newAppsFx(t)
	aid := f.makeApp(t, "web")
	appid, _ := parseID(aid)
	m := &store.Deploy{AppID: appid, Trigger: "manual", Status: "failed"}
	require.NoError(t, f.db.InsertDeploy(context.Background(), m))
	did := itoa(m.ID)
	l, err := deploy.OpenDeployLog(f.dataDir, m.ID, nil)
	require.NoError(t, err)
	for _, s := range []string{"a", "b", "c"} {
		require.NoError(t, l.Line(s))
	}
	// Status terminal, mas o log ainda não foi fechado: done = false.
	code, b := f.call(t, http.MethodGet, "/api/deploys/"+did+"/log?from=1", nil)
	require.Equal(t, http.StatusOK, code)
	es := b["entries"].([]any)
	require.Len(t, es, 2)
	assert.EqualValues(t, 2, es[0].(map[string]any)["n"])
	assert.Equal(t, "b", es[0].(map[string]any)["text"])
	assert.Equal(t, false, b["done"])
	require.NoError(t, l.Close())
	_, b = f.call(t, http.MethodGet, "/api/deploys/"+did+"/log?from=3", nil)
	assert.Len(t, b["entries"].([]any), 0)
	assert.Equal(t, true, b["done"])
	for _, bad := range []string{"-1", "x"} {
		code, b = f.call(t, http.MethodGet, "/api/deploys/"+did+"/log?from="+bad, nil)
		require.Equal(t, http.StatusBadRequest, code)
		assert.Equal(t, "INVALID_FROM", errCode(b))
	}
	// Mais de 5000 linhas: corta e ainda não terminou.
	m2 := &store.Deploy{AppID: appid, Trigger: "manual", Status: "failed"}
	require.NoError(t, f.db.InsertDeploy(context.Background(), m2))
	l2, err := deploy.OpenDeployLog(f.dataDir, m2.ID, nil)
	require.NoError(t, err)
	for i := 0; i < 5003; i++ {
		require.NoError(t, l2.Line("x"))
	}
	require.NoError(t, l2.Close())
	_, b = f.call(t, http.MethodGet, "/api/deploys/"+itoa(m2.ID)+"/log?from=0", nil)
	assert.Len(t, b["entries"].([]any), 5000)
	assert.Equal(t, false, b["done"])
}

func TestDeploysAPI_RollbackErrors(t *testing.T) {
	f := newAppsFx(t)
	aid := f.makeApp(t, "web")
	appid, _ := parseID(aid)

	// Deploy sem imagem guardada → IMAGE_GONE.
	m := &store.Deploy{AppID: appid, Trigger: "manual", Status: "failed"}
	require.NoError(t, f.db.InsertDeploy(context.Background(), m))
	code, b := f.call(t, http.MethodPost, "/api/apps/"+aid+"/rollback", map[string]any{"deployId": m.ID})
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "IMAGE_GONE", errCode(b))

	code, b = f.call(t, http.MethodPost, "/api/apps/"+aid+"/rollback", map[string]any{"deployId": 999})
	require.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "DEPLOY_NOT_FOUND", errCode(b))
	code, b = f.call(t, http.MethodPost, "/api/apps/999/rollback", map[string]any{"deployId": 1})
	require.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "APP_NOT_FOUND", errCode(b))
	code, b = f.call(t, http.MethodPost, "/api/apps/"+aid+"/rollback", map[string]any{"deployId": 0})
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "INVALID_DEPLOY_ID", errCode(b))

	// Compose → ROLLBACK_NOT_SUPPORTED; github sem token no serviço → NO_GITHUB.
	f.gh = true
	code, b = f.call(t, http.MethodPost, "/api/apps", map[string]any{"projectId": f.project(t), "slug": "stack",
		"sourceType": "github", "repo": "dono/stack", "branch": "main", "buildType": "compose",
		"composeService": "web", "internalPort": 80})
	require.Equal(t, http.StatusCreated, code, b)
	cid := itoa(appID(b))
	code, b = f.call(t, http.MethodPost, "/api/apps/"+cid+"/rollback", map[string]any{"deployId": m.ID})
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "ROLLBACK_NOT_SUPPORTED", errCode(b))
	code, b = f.call(t, http.MethodPost, "/api/apps/"+cid+"/deploys", nil)
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "NO_GITHUB", errCode(b))

	// Rollback válido → 202.
	require.NoError(t, f.db.SetDeployResult(context.Background(), m.ID, "korego/web:1", 20001, "c", "", ""))
	require.NoError(t, f.db.UpdateDeployStatus(context.Background(), m.ID, "superseded", "", time.Now()))
	code, b = f.call(t, http.MethodPost, "/api/apps/"+aid+"/rollback", map[string]any{"deployId": m.ID})
	require.Equal(t, http.StatusAccepted, code, b)
	assert.Equal(t, "rollback", b["deploy"].(map[string]any)["trigger"])
	assert.EqualValues(t, m.ID, b["deploy"].(map[string]any)["rollbackOf"])
}

func parseID(s string) (int64, error) {
	var n int64
	for _, c := range s {
		n = n*10 + int64(c-'0')
	}
	return n, nil
}
