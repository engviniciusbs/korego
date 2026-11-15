package api_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/domains"
	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/proxy/proxytest"
	"github.com/engviniciusbs/korego/internal/store"
)

// adoptFx: API com deploys, domínios, raiz de sites temporária e um token.
type adoptFx struct {
	*gateFx
	www string
	// stacks: projeto compose → containers devolvidos pelo Docker falso.
	stacks map[string][]docker.Container
}

func newAdoptFx(t *testing.T) *adoptFx {
	t.Helper()
	www, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	a := &adoptFx{www: www, stacks: map[string][]docker.Container{}}
	g := &gateFx{}
	g.appsFx = newAppsFx(t, func(f *appsFx, d *api.Deps) {
		px := &proxytest.Fake{}
		d.Domains = &domains.Service{DB: f.db, Proxy: px, Hub: events.NewHub(), PanelPort: 8000, ListenAddr: ":8000"}
		d.TokenLimiter = auth.NewLimiter(5, time.Minute, 15*time.Minute)
		d.WWWRoot = www
	})
	g.dock.ListByLabelFn = func(_ context.Context, key, value string) ([]docker.Container, error) {
		if key == "com.docker.compose.project" {
			return a.stacks[value], nil
		}
		return nil, nil
	}
	ctx := context.Background()
	u, err := g.env.svc.DB.UserByEmail(ctx, email)
	require.NoError(t, err)
	g.user = u
	tokens := &auth.Tokens{DB: g.env.svc.DB, Now: g.env.svc.Now}
	_, g.tok, err = tokens.Issue(ctx, u.ID, "agente-ci", 0)
	require.NoError(t, err)
	g.disp = api.NewDispatcherForTest(g.env.deps)
	a.gateFx = g
	return a
}

// site monta <www>/<name>/releases/<versões> com current apontando para live.
func (a *adoptFx) site(t *testing.T, name, live string, others ...string) (root, current string) {
	t.Helper()
	root = filepath.Join(a.www, name)
	for _, r := range append([]string{live}, others...) {
		dir := filepath.Join(root, "releases", r)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte(r), 0o644))
	}
	current = filepath.Join(root, "current")
	require.NoError(t, os.Symlink(filepath.Join(root, "releases", live), current))
	return root, current
}

// stack registra uma stack compose com pasta dir e o arquivo compose.
func (a *adoptFx) stack(project, dir string) {
	a.stacks[project] = []docker.Container{{ID: "c1", Name: project + "-web-1", CreatedAt: time.Now(),
		Labels: map[string]string{
			"com.docker.compose.project":              project,
			"com.docker.compose.project.working_dir":  dir,
			"com.docker.compose.project.config_files": filepath.Join(dir, "docker-compose.yml"),
		}}}
}

func (a *adoptFx) stackDir(t *testing.T, project string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deploy.sh"), []byte("#!/bin/sh\n"), 0o755))
	a.stack(project, dir)
	return dir
}

func staticRoute(path, root string, spa bool, prefix string) store.DomainRoute {
	return store.DomainRoute{Path: path, TargetType: "static", Root: root, SPA: spa, ImmutablePrefix: prefix}
}

func (a *adoptFx) domain(t *testing.T, host string, routes ...store.DomainRoute) store.Domain {
	t.Helper()
	for i := range routes {
		routes[i].Position = i
	}
	d := store.Domain{Hostname: host, WWWRedirect: "none", ForceHTTPS: true, TLSMode: "auto", Routes: routes}
	require.NoError(t, a.db.InsertDomain(context.Background(), &d))
	return d
}

func adoptStaticBody(pid int64, slug, current string) map[string]any {
	return map[string]any{"projectId": pid, "slug": slug, "current": current, "sourceType": "git",
		"repo": "https://github.com/o/r.git", "branch": "main"}
}

func composeBody(pid int64, slug, project string, kv ...string) map[string]any {
	m := map[string]any{"projectId": pid, "slug": slug, "composeProject": project}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

func (a *adoptFx) commands() []string {
	a.runner.mu.Lock()
	defer a.runner.mu.Unlock()
	var out []string
	for _, c := range a.runner.cmds {
		out = append(out, c.Name+" "+strings.Join(c.Args, " "))
	}
	return out
}

func TestAdoptStatic_SwitchesDomainsAndKeepsReleases(t *testing.T) {
	a := newAdoptFx(t)
	ctx := context.Background()
	pid := a.project(t)
	live, old, odd := "20260201000000-1234567", "20260101000000-abcdef1", "versao-manual"
	root, current := a.site(t, "hoprs", live, old, odd)
	other := filepath.Join(a.www, "outro")
	require.NoError(t, os.MkdirAll(other, 0o755))
	hop := a.domain(t, "hoprs.exemplo.com",
		staticRoute("/", current, true, "/static/"),
		store.DomainRoute{Path: "/api/", TargetType: "address", Address: "127.0.0.1:5000"},
		staticRoute("/v/", filepath.Join(root, "releases", live), false, ""))
	outro := a.domain(t, "outro.exemplo.com", staticRoute("/", other, false, ""))

	code, b := a.call(t, http.MethodPost, "/api/adopt/static", adoptStaticBody(pid, "hoprs", current))
	require.Equal(t, http.StatusCreated, code, b)
	assert.Equal(t, []any{"hoprs.exemplo.com"}, b["domains"])
	app := b["app"].(map[string]any)
	id := int64(app["id"].(float64))
	assert.Equal(t, false, app["autoDeploy"])
	assert.Equal(t, root, app["publishRoot"])
	assert.Equal(t, "static", app["buildType"])
	assert.Equal(t, true, app["staticSpa"], "SPA copiado da rota adotada")
	assert.Equal(t, "/static/", app["staticImmutablePrefix"])
	cur := app["currentDeploy"].(map[string]any)
	assert.Equal(t, "running", cur["status"])
	assert.Equal(t, "1234567", cur["commitSha"])

	// O domínio passa para a app; as outras rotas e o outro domínio ficam.
	got, err := a.db.GetDomain(ctx, hop.ID)
	require.NoError(t, err)
	require.Len(t, got.Routes, 3)
	assert.Equal(t, "app", got.Routes[0].TargetType)
	require.NotNil(t, got.Routes[0].AppID)
	assert.Equal(t, id, *got.Routes[0].AppID)
	assert.Equal(t, "", got.Routes[0].Root)
	assert.Equal(t, "address", got.Routes[1].TargetType)
	assert.Equal(t, "127.0.0.1:5000", got.Routes[1].Address)
	assert.Equal(t, "app", got.Routes[2].TargetType, "rota para a mesma versão também é adotada")
	o, err := a.db.GetDomain(ctx, outro.ID)
	require.NoError(t, err)
	assert.Equal(t, "static", o.Routes[0].TargetType)
	assert.Equal(t, other, o.Routes[0].Root)

	// O histórico tem o deploy "adopt" com a pasta da versão no ar.
	ds, err := a.db.ListDeploys(ctx, id, 10)
	require.NoError(t, err)
	require.Len(t, ds, 1)
	assert.Equal(t, "adopt", ds[0].Trigger)
	assert.Equal(t, "running", ds[0].Status)
	assert.Equal(t, filepath.Join(root, "releases", live), ds[0].ReleaseDir)
	code, lb := a.call(t, http.MethodGet, "/api/apps/"+itoa(id)+"/deploys", nil)
	require.Equal(t, http.StatusOK, code, lb)
	assert.Contains(t, lb["deploys"].([]any)[0].(map[string]any)["trigger"], "adopt")

	// Nada mudou no disco.
	for _, r := range []string{live, old, odd} {
		_, err := os.Stat(filepath.Join(root, "releases", r, "index.html"))
		assert.NoError(t, err, r)
	}
	dest, err := os.Readlink(current)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "releases", live), dest)
	assert.NotEmpty(t, audits(t, a.env, a.cookie, "app.adopted"))
}

func TestAdoptStatic_RejectsBadLayout(t *testing.T) {
	a := newAdoptFx(t)
	pid := a.project(t)

	// current é uma pasta, não um link.
	dirCur := filepath.Join(a.www, "pasta", "current")
	require.NoError(t, os.MkdirAll(dirCur, 0o755))
	code, b := a.call(t, http.MethodPost, "/api/adopt/static", adoptStaticBody(pid, "pasta", dirCur))
	require.Equal(t, http.StatusUnprocessableEntity, code, b)
	assert.Equal(t, "INVALID_LAYOUT", errCode(b))

	// current aponta para fora de releases/.
	root := filepath.Join(a.www, "solto")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "build"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(root, "build"), filepath.Join(root, "current")))
	code, b = a.call(t, http.MethodPost, "/api/adopt/static", adoptStaticBody(pid, "solto", filepath.Join(root, "current")))
	require.Equal(t, http.StatusUnprocessableEntity, code, b)
	assert.Equal(t, "INVALID_LAYOUT", errCode(b))

	// Fora da raiz dos sites.
	out := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(out, "releases", "r1"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(out, "releases", "r1"), filepath.Join(out, "current")))
	code, b = a.call(t, http.MethodPost, "/api/adopt/static", adoptStaticBody(pid, "fora", filepath.Join(out, "current")))
	require.Equal(t, http.StatusUnprocessableEntity, code, b)

	apps, err := a.db.ListApps(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, apps)
}

func TestAdoptStatic_Twice(t *testing.T) {
	a := newAdoptFx(t)
	pid := a.project(t)
	_, current := a.site(t, "hoprs", "20260201000000-1234567")
	code, b := a.call(t, http.MethodPost, "/api/adopt/static", adoptStaticBody(pid, "hoprs", current))
	require.Equal(t, http.StatusCreated, code, b)

	code, b = a.call(t, http.MethodPost, "/api/adopt/static", adoptStaticBody(pid, "hoprs-2", current))
	require.Equal(t, http.StatusConflict, code, b)
	assert.Equal(t, "ALREADY_ADOPTED", errCode(b))
}

func TestAdoptCompose_ScriptNeedsCode(t *testing.T) {
	a := newAdoptFx(t)
	pid := a.project(t)
	a.stackDir(t, "mikos")
	body := composeBody(pid, "mikos", "mikos", "updateMode", "script", "updateScript", "deploy.sh")

	st, b := a.as(t, http.MethodPost, "/api/adopt/compose", body, "")
	require.Equal(t, http.StatusForbidden, st, b)
	assert.Equal(t, "CODE_REQUIRED", errCode(b))
	_, err := a.db.AppByComposeProject(context.Background(), "mikos")
	require.ErrorIs(t, err, store.ErrNotFound)

	// Pela sessão, o código vem no corpo.
	code, b := a.call(t, http.MethodPost, "/api/adopt/compose", body)
	require.Equal(t, http.StatusForbidden, code, b)
	assert.Equal(t, "CODE_REQUIRED", errCode(b))

	st, b = a.as(t, http.MethodPost, "/api/adopt/compose", body, a.freshCode(t))
	require.Equal(t, http.StatusCreated, st, b)
	app := b["app"].(map[string]any)
	assert.Equal(t, "local", app["sourceType"])
	assert.Equal(t, "compose-local", app["buildType"])
	assert.Equal(t, "script", app["updateMode"])
	assert.Equal(t, "deploy.sh", app["updateScript"])
	assert.Nil(t, app["currentDeploy"], "a adoção compose não cria deploy")

	// Modo pull (padrão) não pede código; a mesma stack não é adotada duas vezes.
	a.stackDir(t, "loja")
	st, b = a.as(t, http.MethodPost, "/api/adopt/compose", composeBody(pid, "loja", "loja"), "")
	require.Equal(t, http.StatusCreated, st, b)
	assert.Equal(t, "pull", b["app"].(map[string]any)["updateMode"])
	st, b = a.as(t, http.MethodPost, "/api/adopt/compose", composeBody(pid, "loja-2", "loja"), "")
	require.Equal(t, http.StatusConflict, st, b)
	assert.Equal(t, "ALREADY_ADOPTED", errCode(b))

	// Script fora da pasta da stack é recusado antes do código.
	a.stackDir(t, "fora")
	st, b = a.as(t, http.MethodPost, "/api/adopt/compose",
		composeBody(pid, "fora", "fora", "updateMode", "script", "updateScript", "../x.sh"), "")
	require.Equal(t, http.StatusBadRequest, st, b)
	assert.Equal(t, "updateScript", b["error"].(map[string]any)["field"])
	assert.Empty(t, a.commands(), "a adoção não roda nada na stack")
}

func TestAdoptCompose_NoContainers(t *testing.T) {
	a := newAdoptFx(t)
	pid := a.project(t)

	code, b := a.call(t, http.MethodPost, "/api/adopt/compose", composeBody(pid, "nada", "nada"))
	require.Equal(t, http.StatusUnprocessableEntity, code, b)
	assert.Equal(t, "STACK_NOT_FOUND", errCode(b))
	assert.Contains(t, b["error"].(map[string]any)["message"], "nada")

	a.stacks["semrotulo"] = []docker.Container{{ID: "c1", Labels: map[string]string{"com.docker.compose.project": "semrotulo"}}}
	code, b = a.call(t, http.MethodPost, "/api/adopt/compose", composeBody(pid, "semrotulo", "semrotulo"))
	require.Equal(t, http.StatusUnprocessableEntity, code, b)
	assert.Equal(t, "STACK_LABELS", errCode(b))

	toRoot := filepath.Join(t.TempDir(), "raiz")
	require.NoError(t, os.Symlink("/", toRoot))
	for i, dir := range []string{"/etc", "/etc/", "/var/.", "/", toRoot, filepath.Join(t.TempDir(), "sumiu")} {
		p := "sistema" + itoa(int64(i))
		a.stack(p, dir)
		code, b = a.call(t, http.MethodPost, "/api/adopt/compose", composeBody(pid, p, p))
		require.Equal(t, http.StatusUnprocessableEntity, code, "%s: %v", dir, b)
		assert.Equal(t, "STACK_DIR_UNSAFE", errCode(b), dir)
	}

	apps, err := a.db.ListApps(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, apps)
}

// adoptPull adota uma stack em modo pull pela sessão e devolve o id da app.
func (a *adoptFx) adoptPull(t *testing.T, project string) (int64, string) {
	t.Helper()
	dir := a.stackDir(t, project)
	code, b := a.call(t, http.MethodPost, "/api/adopt/compose", composeBody(a.project(t), project, project))
	require.Equal(t, http.StatusCreated, code, b)
	return appID(b), dir
}

func TestStackStop_Gated(t *testing.T) {
	a := newAdoptFx(t)
	id, dir := a.adoptPull(t, "mikos")
	path := "/api/apps/" + itoa(id) + "/stack/"

	st, b := a.as(t, http.MethodPost, path+"stop", nil, "")
	require.Equal(t, http.StatusForbidden, st, b)
	assert.Equal(t, "CODE_REQUIRED", errCode(b))
	code, b := a.call(t, http.MethodPost, path+"stop", nil)
	require.Equal(t, http.StatusForbidden, code, b)
	assert.Equal(t, "CODE_REQUIRED", errCode(b))
	assert.Empty(t, a.commands())

	st, b = a.as(t, http.MethodPost, path+"start", nil, "")
	require.Equal(t, http.StatusNoContent, st, b)
	st, b = a.as(t, http.MethodPost, path+"stop", nil, a.freshCode(t))
	require.Equal(t, http.StatusNoContent, st, b)
	code, b = a.call(t, http.MethodPost, path+"stop", map[string]any{"code": a.freshCode(t)})
	require.Equal(t, http.StatusNoContent, code, b)
	compose := "docker compose -p mikos -f " + filepath.Join(dir, "docker-compose.yml")
	assert.Equal(t, []string{compose + " start", compose + " stop", compose + " stop"}, a.commands())

	st, b = a.as(t, http.MethodPost, path+"down", nil, "")
	require.Equal(t, http.StatusBadRequest, st, b)

	ops := []string{}
	for _, m := range audits(t, a.env, a.cookie, "app.stack_action") {
		if m["ok"] == true {
			ops = append(ops, m["op"].(string))
		}
	}
	assert.ElementsMatch(t, []string{"start", "stop", "stop"}, ops)
}

func TestUpdateScriptChange_Gated(t *testing.T) {
	a := newAdoptFx(t)
	id, _ := a.adoptPull(t, "mikos")
	path := "/api/apps/" + itoa(id)
	put := func(script string) map[string]any {
		return map[string]any{"projectId": a.project(t), "slug": "mikos", "sourceType": "local",
			"buildType": "compose-local", "updateMode": "script", "updateScript": script}
	}

	st, b := a.as(t, http.MethodPut, path, put("deploy.sh"), "")
	require.Equal(t, http.StatusForbidden, st, b)
	assert.Equal(t, "CODE_REQUIRED", errCode(b))
	code, b := a.call(t, http.MethodPut, path, put("deploy.sh"))
	require.Equal(t, http.StatusForbidden, code, b)
	got, err := a.db.GetApp(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "pull", got.UpdateMode)

	st, b = a.as(t, http.MethodPut, path, put("deploy.sh"), a.freshCode(t))
	require.Equal(t, http.StatusOK, st, b)
	assert.Equal(t, "script", b["app"].(map[string]any)["updateMode"])

	// Sem mudança no script: não pede código.
	st, b = a.as(t, http.MethodPut, path, put("deploy.sh"), "")
	require.Equal(t, http.StatusOK, st, b)

	// Mudar o caminho pede de novo; sessão manda no corpo.
	body := put("./deploy.sh")
	code, b = a.call(t, http.MethodPut, path, body)
	require.Equal(t, http.StatusForbidden, code, b)
	body["code"] = a.freshCode(t)
	code, b = a.call(t, http.MethodPut, path, body)
	require.Equal(t, http.StatusOK, code, b)

	// Fora da pasta da stack: recusado ao salvar.
	st, b = a.as(t, http.MethodPut, path, put("../x.sh"), a.freshCode(t))
	require.Equal(t, http.StatusBadRequest, st, b)
	assert.Equal(t, "updateScript", b["error"].(map[string]any)["field"])

	// Uma stack não vira outra origem.
	st, b = a.as(t, http.MethodPut, path, imageApp(a.project(t), "mikos"), "")
	require.Equal(t, http.StatusBadRequest, st, b)
}

func TestLocalEnv_NamesOnly(t *testing.T) {
	a := newAdoptFx(t)
	id, dir := a.adoptPull(t, "mikos")
	envPath := filepath.Join(dir, ".env")
	orig := "# banco\nDB_URL=postgres://u:segredo@h/db\n\nexport TOKEN='valor-secreto'\n"
	require.NoError(t, os.WriteFile(envPath, []byte(orig), 0o640))
	path := "/api/apps/" + itoa(id) + "/env"

	w := a.do(t, http.MethodGet, path, nil, withCookie(a.cookie))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), "segredo")
	assert.Equal(t, []any{"DB_URL", "TOKEN"}, decode(t, w)["names"])

	code, b := a.call(t, http.MethodPut, path, map[string]any{"set": map[string]string{"TOKEN": "novo", "NOVA": "x"}})
	require.Equal(t, http.StatusNoContent, code, b)
	got, err := os.ReadFile(envPath)
	require.NoError(t, err)
	assert.Equal(t, "# banco\nDB_URL=postgres://u:segredo@h/db\n\nexport TOKEN=novo\nNOVA=x\n", string(got))
	fi, err := os.Stat(envPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), fi.Mode().Perm())
	names, err := a.db.AppEnvNames(context.Background(), id)
	require.NoError(t, err)
	assert.Empty(t, names, "nada na tabela app_env")

	code, b = a.call(t, http.MethodPut, path, map[string]any{"remove": []string{"TOKEN"}})
	require.Equal(t, http.StatusBadRequest, code, b)
	code, b = a.call(t, http.MethodPut, path, map[string]any{"set": map[string]string{"X": "a\nb"}})
	require.Equal(t, http.StatusBadRequest, code, b)
	assert.Empty(t, a.commands(), "gravar o .env não reinicia nada")
}

func TestDeleteLocal_NoDown(t *testing.T) {
	a := newAdoptFx(t)
	id, dir := a.adoptPull(t, "mikos")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("A=1\n"), 0o600))

	code, b := a.call(t, http.MethodDelete, "/api/apps/"+itoa(id), nil)
	require.Equal(t, http.StatusNoContent, code, b)
	for _, c := range a.commands() {
		assert.NotContains(t, c, "down", c)
	}
	assert.Empty(t, a.removed, "nenhum container removido")
	_, err := os.Stat(filepath.Join(dir, ".env"))
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "deploy.sh"))
	assert.NoError(t, err)
}

func TestMCP_AdoptToolsListed(t *testing.T) {
	m := newMCPFx(t, true)
	tools := listTools(t, m.connect(t))
	for _, name := range []string{"app_adopt_static", "app_adopt_compose", "app_compose_action"} {
		require.Contains(t, tools, name)
	}
	assert.Contains(t, schemaProps(t, tools["app_update"].InputSchema), "code")
	assert.Contains(t, schemaProps(t, tools["app_adopt_compose"].InputSchema), "code")
	action := schemaProps(t, tools["app_compose_action"].InputSchema)
	assert.Contains(t, action, "op")
	assert.Contains(t, action, "code")
	body := schemaProps(t, schemaProps(t, tools["app_adopt_static"].InputSchema)["body"])
	assert.Contains(t, body, "current")
	assert.NotContains(t, schemaProps(t, schemaProps(t, tools["app_adopt_compose"].InputSchema)["body"]), "code",
		"o código vai fora do corpo")
}

func TestStackAction_FailureHidesOutput(t *testing.T) {
	a := newAdoptFx(t)
	id, _ := a.adoptPull(t, "mikos")
	a.runner.mu.Lock()
	a.runner.out, a.runner.err = "DB_PASSWORD=segredo no log do compose", errors.New("exit status 1")
	a.runner.mu.Unlock()

	code, b := a.call(t, http.MethodPost, "/api/apps/"+itoa(id)+"/stack/restart", nil)
	require.Equal(t, http.StatusBadGateway, code, b)
	assert.Equal(t, "STACK_ACTION_FAILED", errCode(b))
	msg := b["error"].(map[string]any)["message"].(string)
	assert.NotContains(t, msg, "segredo")
	assert.NotContains(t, msg, "exit status")
	assert.Contains(t, msg, "restart")
	recs := audits(t, a.env, a.cookie, "app.stack_action")
	require.NotEmpty(t, recs)
	for _, m := range recs {
		assert.Equal(t, map[string]any{"op": "restart", "ok": false}, m)
	}
}
