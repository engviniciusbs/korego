package api_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/deploy"
	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/jobs"
	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/docker/dockertest"
	"github.com/engviniciusbs/korego/internal/secrets"
	"github.com/engviniciusbs/korego/internal/store"
)

// recRunner grava os comandos que a API manda executar.
type recRunner struct {
	mu   sync.Mutex
	cmds []deploy.Cmd
}

func (r *recRunner) Run(_ context.Context, c deploy.Cmd, _ io.Writer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds = append(r.cmds, c)
	return nil
}

type appsFx struct {
	*env
	db      *store.DB
	dock    *dockertest.Fake
	runner  *recRunner
	dataDir string
	svc     *deploy.Service
	cookie  *http.Cookie
	csrf    string
	release chan struct{}
	gh      bool
	removed []string
}

func (f *appsFx) mut() []reqOpt { return mut(f.cookie, f.csrf) }

func (f *appsFx) call(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	w := f.do(t, method, path, body, f.mut()...)
	if w.Body.Len() == 0 {
		return w.Code, nil
	}
	return w.Code, decode(t, w)
}

func newAppsFx(t *testing.T) *appsFx {
	t.Helper()
	f := &appsFx{db: store.OpenMemory(t), runner: &recRunner{}, release: make(chan struct{})}
	box, err := secrets.NewBox(make([]byte, 32))
	require.NoError(t, err)
	f.dock = &dockertest.Fake{
		// O pull bloqueia: o deploy fica "building" até cancelar ou liberar.
		PullFn: func(ctx context.Context, _ string, _ func(string)) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-f.release:
				return docker.ErrUnavailable
			}
		},
		ListByLabelFn: func(context.Context, string, string) ([]docker.Container, error) { return nil, nil },
		ImagesFn:      func(context.Context, string) ([]docker.Image, error) { return nil, nil },
		RemoveForceFn: func(_ context.Context, id string) error { f.removed = append(f.removed, id); return nil },
	}
	f.dataDir = t.TempDir()
	q := jobs.NewQueue(2)
	t.Cleanup(q.Close)
	f.svc = &deploy.Service{DB: f.db, Docker: f.dock, Runner: f.runner, Box: box, Hub: events.NewHub(),
		Queue: q, DataDir: f.dataDir}
	f.env = newEnvDeps(t, api.Deps{
		DB: f.db, Deploys: f.svc, DataDir: f.dataDir, Runner: f.runner, Box: box,
		GitHubConnected: func(context.Context) bool { return f.gh },
	})
	_, f.cookie, f.csrf = f.setup(t)
	return f
}

func (f *appsFx) project(t *testing.T) int64 {
	t.Helper()
	p := &store.Project{Name: "proj" + strconv.Itoa(len(f.removed)) + time.Now().Format("150405.000000000")}
	require.NoError(t, f.db.InsertProject(context.Background(), p))
	return p.ID
}

func imageApp(pid int64, slug string) map[string]any {
	return map[string]any{"projectId": pid, "slug": slug, "sourceType": "image", "image": "nginx:1-alpine",
		"buildType": "image", "internalPort": 80}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func appID(b map[string]any) int64 { return int64(b["app"].(map[string]any)["id"].(float64)) }

func TestAppsAPI_ValidationPerSource(t *testing.T) {
	f := newAppsFx(t)
	pid := f.project(t)
	f.gh = true
	gh := func() map[string]any {
		return map[string]any{"projectId": pid, "slug": "blog", "sourceType": "github", "repo": "dono/blog",
			"branch": "main", "buildType": "dockerfile", "internalPort": 3000}
	}
	with := func(m map[string]any, kv ...any) map[string]any {
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	cases := []struct {
		name  string
		in    map[string]any
		field string
	}{
		{"github sem branch", with(gh(), "branch", ""), "branch"},
		{"github repo inválido", with(gh(), "repo", "sem-barra"), "repo"},
		{"image sem imagem", with(imageApp(pid, "x"), "image", ""), "image"},
		{"image com buildType errado", with(imageApp(pid, "x"), "buildType", "dockerfile"), "buildType"},
		{"compose sem service", with(gh(), "buildType", "compose", "composeService", ""), "composeService"},
		{"slug inválido", with(gh(), "slug", "Slug_Ruim"), "slug"},
		{"porta zero", with(gh(), "internalPort", 0), "internalPort"},
		{"compose sem porta", with(gh(), "buildType", "compose", "composeService", "web", "internalPort", 0), "internalPort"},
		{"git com http", with(gh(), "sourceType", "git", "repo", "http://x.exemplo.com/a.git"), "repo"},
		{"sourceType ruim", with(gh(), "sourceType", "svn"), "sourceType"},
		{"buildType ruim", with(gh(), "buildType", "make"), "buildType"},
		{"projeto inexistente", with(gh(), "projectId", 999), "projectId"},
		{"health fora da faixa", with(gh(), "healthTimeoutS", 5), "healthTimeoutS"},
		{"dockerfilePath com ..", with(gh(), "dockerfilePath", "../x"), "dockerfilePath"},
	}
	for _, c := range cases {
		code, b := f.call(t, http.MethodPost, "/api/apps", c.in)
		require.Equal(t, http.StatusBadRequest, code, c.name)
		er := b["error"].(map[string]any)
		assert.Equal(t, "INVALID_APP", er["code"], c.name)
		assert.Equal(t, c.field, er["field"], c.name)
	}

	// Válidos: github com defaults, git, compose e image.
	code, b := f.call(t, http.MethodPost, "/api/apps", gh())
	require.Equal(t, http.StatusCreated, code, b)
	app := b["app"].(map[string]any)
	assert.Equal(t, "Dockerfile", app["dockerfilePath"])
	assert.EqualValues(t, 60, app["healthTimeoutS"])
	assert.Equal(t, "", app["url"])
	assert.Nil(t, app["currentDeploy"])
	assert.Equal(t, []any{}, app["domains"])

	code, b = f.call(t, http.MethodPost, "/api/apps", gh())
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "APP_EXISTS", errCode(b))

	comp := with(gh(), "slug", "stack", "buildType", "compose", "composeService", "web")
	code, b = f.call(t, http.MethodPost, "/api/apps", comp)
	require.Equal(t, http.StatusCreated, code, b)
	assert.Equal(t, "docker-compose.yml", b["app"].(map[string]any)["composePath"])

	code, b = f.call(t, http.MethodPost, "/api/apps", with(gh(), "slug", "g1", "sourceType", "git",
		"repo", "https://git.exemplo.com/a/b.git"))
	require.Equal(t, http.StatusCreated, code, b)
	code, b = f.call(t, http.MethodPost, "/api/apps", imageApp(pid, "img"))
	require.Equal(t, http.StatusCreated, code, b)
}

func TestAppsAPI_NoGitHubOnCreate(t *testing.T) {
	f := newAppsFx(t)
	pid := f.project(t)
	in := map[string]any{"projectId": pid, "slug": "blog", "sourceType": "github", "repo": "dono/blog",
		"branch": "main", "buildType": "dockerfile", "internalPort": 3000}
	code, b := f.call(t, http.MethodPost, "/api/apps", in)
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "NO_GITHUB", errCode(b))
}

func TestAppsAPI_UpdateSlugImmutableAndGet(t *testing.T) {
	f := newAppsFx(t)
	pid := f.project(t)
	_, b := f.call(t, http.MethodPost, "/api/apps", imageApp(pid, "web"))
	path := "/api/apps/" + itoa(appID(b))

	code, b := f.call(t, http.MethodPut, path, imageApp(pid, "outro"))
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "slug", b["error"].(map[string]any)["field"])

	in := imageApp(pid, "web")
	in["internalPort"] = 8080
	code, b = f.call(t, http.MethodPut, path, in)
	require.Equal(t, http.StatusOK, code, b)
	assert.EqualValues(t, 8080, b["app"].(map[string]any)["internalPort"])

	code, b = f.call(t, http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, code)
	assert.EqualValues(t, 8080, b["app"].(map[string]any)["internalPort"])

	code, b = f.call(t, http.MethodGet, "/api/apps/999", nil)
	require.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "APP_NOT_FOUND", errCode(b))
	code, b = f.call(t, http.MethodGet, "/api/apps/abc", nil)
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "INVALID_APP_ID", errCode(b))
}

func TestAppsAPI_EnvNeverReturnsValues(t *testing.T) {
	f := newAppsFx(t)
	pid := f.project(t)
	_, b := f.call(t, http.MethodPost, "/api/apps", imageApp(pid, "web"))
	id := itoa(appID(b))
	const secret = "s3cr3t-valor"

	code, _ := f.call(t, http.MethodPut, "/api/apps/"+id+"/env", map[string]any{
		"set": map[string]string{"DB_PASSWORD": secret, "VAZIA": ""}})
	require.Equal(t, http.StatusNoContent, code)

	code, b = f.call(t, http.MethodGet, "/api/apps/"+id+"/env", nil)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, []any{"DB_PASSWORD", "VAZIA"}, b["names"])

	for _, p := range []string{"/api/apps/" + id + "/env", "/api/apps/" + id, "/api/apps", "/api/audit"} {
		w := f.do(t, http.MethodGet, p, nil, withCookie(f.cookie))
		require.Equal(t, http.StatusOK, w.Code, p)
		assert.NotContains(t, w.Body.String(), secret, p)
	}
	w := f.do(t, http.MethodGet, "/api/audit", nil, withCookie(f.cookie))
	assert.Contains(t, w.Body.String(), "app.env")
	assert.Contains(t, w.Body.String(), "DB_PASSWORD")

	// Remover e validar.
	code, _ = f.call(t, http.MethodPut, "/api/apps/"+id+"/env", map[string]any{"remove": []string{"VAZIA"}})
	require.Equal(t, http.StatusNoContent, code)
	_, b = f.call(t, http.MethodGet, "/api/apps/"+id+"/env", nil)
	assert.Equal(t, []any{"DB_PASSWORD"}, b["names"])

	code, b = f.call(t, http.MethodPut, "/api/apps/"+id+"/env", map[string]any{"set": map[string]string{"1ruim": "x"}})
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "INVALID_ENV", errCode(b))
	assert.Equal(t, "1ruim", b["error"].(map[string]any)["field"])

	code, b = f.call(t, http.MethodPut, "/api/apps/"+id+"/env", map[string]any{
		"set": map[string]string{"GRANDE": strings.Repeat("x", 32*1024+1)}})
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "INVALID_ENV", errCode(b))

	many := map[string]string{}
	for i := 0; i < 201; i++ {
		many["V"+strconv.Itoa(i)] = "x"
	}
	code, b = f.call(t, http.MethodPut, "/api/apps/"+id+"/env", map[string]any{"set": many})
	require.Equal(t, http.StatusBadRequest, code)
	assert.Equal(t, "INVALID_ENV", errCode(b))

	code, b = f.call(t, http.MethodPut, "/api/apps/999/env", map[string]any{})
	require.Equal(t, http.StatusNotFound, code)
	assert.Equal(t, "APP_NOT_FOUND", errCode(b))
}

func TestAppsAPI_DeleteWithDomains409(t *testing.T) {
	f := newAppsFx(t)
	pid := f.project(t)
	_, b := f.call(t, http.MethodPost, "/api/apps", imageApp(pid, "web"))
	id := appID(b)
	aid := id
	require.NoError(t, f.db.InsertDomain(context.Background(), &store.Domain{
		Hostname: "app.exemplo.com", TargetType: "app", AppID: &aid, WWWRedirect: "none"}))

	_, b = f.call(t, http.MethodGet, "/api/apps/"+itoa(id), nil)
	assert.Equal(t, []any{"app.exemplo.com"}, b["app"].(map[string]any)["domains"])

	code, b := f.call(t, http.MethodDelete, "/api/apps/"+itoa(id), nil)
	require.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "APP_HAS_DOMAINS", errCode(b))
	_, err := f.db.GetApp(context.Background(), id)
	assert.NoError(t, err)
}

func TestAppsAPI_DeleteRemovesContainersImagesAndDir(t *testing.T) {
	f := newAppsFx(t)
	pid := f.project(t)
	_, b := f.call(t, http.MethodPost, "/api/apps", imageApp(pid, "web"))
	id := appID(b)
	f.dock.ListByLabelFn = func(_ context.Context, k, v string) ([]docker.Container, error) {
		assert.Equal(t, "korego.app", k)
		assert.Equal(t, "web", v)
		return []docker.Container{{ID: "c1"}, {ID: "c2"}}, nil
	}
	var imgRemoved []string
	f.dock.ImagesFn = func(context.Context, string) ([]docker.Image, error) {
		return []docker.Image{{Ref: "korego/web:1"}}, nil
	}
	f.dock.RemoveImageFn = func(_ context.Context, ref string) error { imgRemoved = append(imgRemoved, ref); return nil }
	dir := deploy.AppDir(f.dataDir, "web")
	require.NoError(t, os.MkdirAll(dir, 0o750))

	code, _ := f.call(t, http.MethodDelete, "/api/apps/"+itoa(id), nil)
	require.Equal(t, http.StatusNoContent, code)
	assert.Equal(t, []string{"c1", "c2"}, f.removed)
	assert.Equal(t, []string{"korego/web:1"}, imgRemoved)
	_, err := os.Stat(dir)
	assert.True(t, os.IsNotExist(err))
	_, err = f.db.GetApp(context.Background(), id)
	assert.ErrorIs(t, err, store.ErrNotFound)
	assert.Empty(t, f.runner.cmds)
}

func TestAppsAPI_DeleteComposeAndFailureKeepsRow(t *testing.T) {
	f := newAppsFx(t)
	pid := f.project(t)
	f.gh = true
	_, b := f.call(t, http.MethodPost, "/api/apps", map[string]any{"projectId": pid, "slug": "stack",
		"sourceType": "github", "repo": "dono/stack", "branch": "main", "buildType": "compose",
		"composeService": "web", "internalPort": 80})
	id := appID(b)
	dir := deploy.AppDir(f.dataDir, "stack")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x"), []byte("1"), 0o600))

	code, _ := f.call(t, http.MethodDelete, "/api/apps/"+itoa(id), nil)
	require.Equal(t, http.StatusNoContent, code)
	require.Len(t, f.runner.cmds, 1)
	c := f.runner.cmds[0]
	assert.Equal(t, "docker", c.Name)
	assert.Equal(t, []string{"compose", "-p", "korego-stack", "down", "--remove-orphans"}, c.Args)
	assert.Equal(t, dir, c.Dir)

	// Falha de Docker antes do DeleteApp: 500 e a linha permanece.
	_, b = f.call(t, http.MethodPost, "/api/apps", imageApp(pid, "web"))
	id2 := appID(b)
	f.dock.ListByLabelFn = func(context.Context, string, string) ([]docker.Container, error) {
		return nil, docker.ErrUnavailable
	}
	code, _ = f.call(t, http.MethodDelete, "/api/apps/"+itoa(id2), nil)
	require.Equal(t, http.StatusInternalServerError, code)
	_, err := f.db.GetApp(context.Background(), id2)
	assert.NoError(t, err)
	w := f.do(t, http.MethodGet, "/api/audit", nil, withCookie(f.cookie))
	assert.Contains(t, w.Body.String(), "app.delete")
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, 5*time.Second, 10*time.Millisecond)
}
