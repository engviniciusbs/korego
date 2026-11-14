package api_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/certs"
	"github.com/engviniciusbs/korego/internal/domains"
	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/github"
	"github.com/engviniciusbs/korego/internal/migrate"
	"github.com/engviniciusbs/korego/internal/node/nodetest"
	"github.com/engviniciusbs/korego/internal/node/proxy/proxytest"
)

// allMCPTools é a tabela de ferramentas da tarefa, com todas as Deps.
var allMCPTools = []string{
	"system_info", "metrics_current", "metrics_history", "usage_current", "usage_history", "usage_at",
	"pm2_list", "audit_list",
	"container_list", "container_get", "container_logs", "container_start", "container_stop",
	"container_restart", "container_remove",
	"github_status", "github_repos", "github_disconnect",
	"project_list", "project_create", "project_update", "project_delete",
	"app_list", "app_get", "app_create", "app_update", "app_delete", "app_env_names", "app_env_set",
	"deploy_start", "deploy_list", "deploy_get", "deploy_log", "deploy_cancel", "deploy_rollback", "deploy_wait",
	"domain_list", "domain_create", "domain_update", "domain_dns", "domain_delete",
	"proxy_status", "proxy_retry", "proxy_trial_check", "proxy_takeover", "proxy_rollback",
	"certificate_list", "certificate_create", "certificate_delete",
	"panel_settings", "panel_set_domain", "panel_set_public_port",
	"update_status", "update_apply",
}

const mcpTokenName = "claude-notebook"

// mcpFx serve o roteador completo por HTTPS, com um token emitido.
type mcpFx struct {
	*appsFx
	srv    *httptest.Server
	secret string // segredo do token
}

// newMCPFx: full monta todas as Deps do núcleo; senão só banco e deploys.
func newMCPFx(t *testing.T, full bool) *mcpFx {
	t.Helper()
	tk := &auth.Tokens{}
	f := newAppsFx(t, func(f *appsFx, d *api.Deps) {
		d.Tokens = tk
		d.TokenLimiter = auth.NewLimiter(5, time.Minute, 15*time.Minute)
		if !full {
			return
		}
		px := &proxytest.Fake{}
		dom := &domains.Service{DB: f.db, Proxy: px, Hub: events.NewHub(), PanelPort: 8000, ListenAddr: ":8000",
			Resolver: fakeResolver{ips: []net.IP{net.ParseIP("203.0.113.10")}},
			HostIPs:  func() ([]net.IP, error) { return []net.IP{net.ParseIP("203.0.113.10")}, nil },
			Ranges:   fixedRanges{"203.0.113.0/24"}}
		d.Domains = dom
		d.Migrate = &migrate.Engine{Domains: dom, Proxy: px, Systemd: &fakeUnits{active: map[string]bool{}}, DB: f.db, Hub: dom.Hub}
		d.Certs = &certs.Service{DB: f.db, Box: d.Box}
		d.Node = &nodetest.Fake{DockerClient: f.dock}
		d.Updater = &fakeUpdater{current: "0.4.0", latest: "v0.5.0"}
		d.GitHub = &github.Service{DB: f.db, Box: d.Box, Client: &github.Client{}}
	})
	// O pull das apps de imagem bloqueia até aqui; libera antes de fechar a fila.
	t.Cleanup(func() { close(f.release) })
	tk.DB, tk.Now = f.env.svc.DB, f.env.svc.Now
	ctx := context.Background()
	u, err := f.env.svc.DB.UserByEmail(ctx, email)
	require.NoError(t, err)
	secret, _, err := tk.Issue(ctx, u.ID, mcpTokenName, 0)
	require.NoError(t, err)
	srv := httptest.NewTLSServer(f.env.h)
	t.Cleanup(srv.Close)
	return &mcpFx{appsFx: f, srv: srv, secret: secret}
}

// headerRT põe o Authorization em toda requisição do cliente MCP.
type headerRT struct {
	base http.RoundTripper
	auth string
	host string      // Host da requisição ("" = o do servidor)
	hdr  http.Header // cabeçalhos extras
}

func (h headerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if h.auth != "" {
		r.Header.Set("Authorization", h.auth)
	}
	for k, v := range h.hdr {
		r.Header[k] = v
	}
	if h.host != "" {
		r.Host = h.host
	}
	return h.base.RoundTrip(r)
}

func (m *mcpFx) connectWith(ctx context.Context, authz string, opts ...*mcp.ClientSessionOptions) (*mcp.ClientSession, error) {
	hc := m.srv.Client()
	hc.Transport = headerRT{base: hc.Transport, auth: authz}
	c := mcp.NewClient(&mcp.Implementation{Name: "teste", Version: "1"}, nil)
	return c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: m.srv.URL + "/mcp", HTTPClient: hc,
		MaxRetries: -1, DisableStandaloneSSE: true}, append(opts, nil)[0])
}

func (m *mcpFx) connect(t *testing.T) *mcp.ClientSession { return m.connectOpts(t, nil) }

func (m *mcpFx) connectOpts(t *testing.T, opts *mcp.ClientSessionOptions) *mcp.ClientSession {
	t.Helper()
	cs, err := m.connectWith(context.Background(), "Bearer "+m.secret, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args any) (*mcp.CallToolResult, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err, name)
	return res, resultText(res)
}

func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// okJSON chama a ferramenta, exige sucesso e decodifica o texto como JSON.
func okJSON(t *testing.T, cs *mcp.ClientSession, name string, args any) (map[string]any, string) {
	t.Helper()
	res, txt := callTool(t, cs, name, args)
	require.False(t, res.IsError, "%s: %s", name, txt)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(txt), &m), "%s: %s", name, txt)
	return m, txt
}

func listTools(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	out := map[string]*mcp.Tool{}
	for tool, err := range cs.Tools(context.Background(), nil) {
		require.NoError(t, err)
		out[tool.Name] = tool
	}
	return out
}

func schemaProps(t *testing.T, s any) map[string]any {
	t.Helper()
	m, ok := s.(map[string]any)
	require.True(t, ok, "schema: %#v", s)
	p, _ := m["properties"].(map[string]any)
	return p
}

func TestMCP_ListTools(t *testing.T) {
	m := newMCPFx(t, true)
	cs := m.connect(t)
	ins := cs.InitializeResult()
	assert.Equal(t, "korego", ins.ServerInfo.Name)
	assert.Contains(t, ins.Instructions, "deploy_wait")
	assert.LessOrEqual(t, len(strings.Split(strings.TrimSpace(ins.Instructions), "\n")), 25)

	tools := listTools(t, cs)
	for _, name := range allMCPTools {
		assert.Contains(t, tools, name)
	}
	assert.Len(t, tools, len(allMCPTools), "nenhuma ferramenta fora da tabela")

	body := schemaProps(t, schemaProps(t, tools["domain_create"].InputSchema)["body"])
	assert.Contains(t, body, "hostname")
	assert.Contains(t, body, "routes")
	assert.Contains(t, schemaProps(t, tools["project_delete"].InputSchema), "code")
	assert.NotContains(t, schemaProps(t, tools["project_list"].InputSchema), "code")
	assert.NotContains(t, schemaProps(t, tools["project_create"].InputSchema), "code")
	for name, tool := range tools {
		assert.NotEmpty(t, tool.Description, name)
	}
}

func TestMCP_ToolsFollowRoutes(t *testing.T) {
	m := newMCPFx(t, false)
	tools := listTools(t, m.connect(t))
	for _, name := range []string{"project_create", "deploy_wait", "usage_history", "system_info", "container_list"} {
		assert.Contains(t, tools, name)
	}
	for name := range tools {
		for _, p := range []string{"domain_", "proxy_", "certificate_", "panel_", "github_", "update_"} {
			assert.False(t, strings.HasPrefix(name, p), "%s sem a rota no núcleo", name)
		}
	}
}

func TestMCP_Unauthorized(t *testing.T) {
	m := newMCPFx(t, false)
	resp, err := m.srv.Client().Post(m.srv.URL+"/mcp", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = m.connectWith(ctx, "")
	require.Error(t, err)
	_, err = m.connectWith(ctx, "Bearer kg_invalido")
	require.Error(t, err)
}

func TestMCP_PublishScenario(t *testing.T) {
	m := newMCPFx(t, true)
	cs := m.connect(t)
	const secretValue = "postgres://usuario:segredo-xyz@db/app"
	var texts []string
	call := func(name string, args any) map[string]any {
		out, txt := okJSON(t, cs, name, args)
		texts = append(texts, txt)
		return out
	}

	p := call("project_create", map[string]any{"body": map[string]any{"name": "site", "description": "do agente"}})
	pid := p["project"].(map[string]any)["id"]
	a := call("app_create", map[string]any{"body": map[string]any{
		"projectId": pid, "slug": "site", "sourceType": "git", "repo": "https://git.exemplo.com/dono/site.git",
		"branch": "main", "buildType": "dockerfile", "internalPort": 3000,
	}})
	aid := a["app"].(map[string]any)["id"]
	call("app_env_set", map[string]any{"id": aid, "body": map[string]any{"set": map[string]string{"DATABASE_URL": secretValue}}})
	names := call("app_env_names", map[string]any{"id": aid})
	assert.Equal(t, []any{"DATABASE_URL"}, names["names"])

	d := call("deploy_start", map[string]any{"id": aid})
	did := d["deploy"].(map[string]any)["id"]
	w := call("deploy_wait", map[string]any{"id": did, "timeoutSeconds": 30})
	assert.Equal(t, false, w["timedOut"], w)
	assert.Contains(t, []any{"running", "failed", "superseded", "cancelled"}, w["status"])
	assert.Contains(t, w, "lastLines")
	call("deploy_log", map[string]any{"id": did})

	dom := call("domain_create", map[string]any{"body": map[string]any{
		"hostname": "site.exemplo.com",
		"routes":   []any{map[string]any{"path": "/", "targetType": "app", "appId": aid}},
	}})
	domID := dom["domain"].(map[string]any)["id"]
	dns := call("domain_dns", map[string]any{"id": domID})
	assert.Contains(t, dns, "ok")
	ps, psTxt := okJSON(t, cs, "proxy_status", nil)
	texts = append(texts, psTxt)
	assert.Contains(t, ps, "proxy")
	assert.Contains(t, psTxt, "site.exemplo.com")
	call("app_get", map[string]any{"id": aid})

	for _, txt := range texts {
		assert.NotContains(t, txt, "segredo-xyz")
	}
	for _, action := range []string{"project.create", "app.create", "app.env", "deploy.create", "domain.create"} {
		rows := audits(t, m.env, m.cookie, action)
		require.NotEmpty(t, rows, action)
		assert.Equal(t, mcpTokenName, rows[0]["token"], action)
		assert.Equal(t, "token", rows[0]["via"], action)
	}
}

// Atrás do Caddy local: conexão de loopback, Host do painel e
// X-Forwarded-Proto https. A proteção de DNS rebinding do SDK não pode recusar.
func TestMCP_BehindLocalProxy(t *testing.T) {
	m := newMCPFx(t, false)
	plain := httptest.NewServer(m.env.h)
	t.Cleanup(plain.Close)
	hc := &http.Client{Transport: headerRT{base: http.DefaultTransport, auth: "Bearer " + m.secret,
		host: "painel.exemplo.com", hdr: http.Header{"X-Forwarded-Proto": {"https"}}}}
	c := mcp.NewClient(&mcp.Implementation{Name: "teste", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: plain.URL + "/mcp",
		HTTPClient: hc, MaxRetries: -1, DisableStandaloneSSE: true}, nil)
	require.NoError(t, err)
	defer cs.Close()
	okJSON(t, cs, "project_list", nil)

	// Pelo mesmo proxy, mas em HTTP puro: o token é recusado.
	hc.Transport = headerRT{base: http.DefaultTransport, auth: "Bearer " + m.secret,
		host: "painel.exemplo.com", hdr: http.Header{"X-Forwarded-Proto": {"http"}}}
	resp, err := hc.Post(plain.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestMCP_ErrorCarriesCode(t *testing.T) {
	m := newMCPFx(t, true)
	cs := m.connect(t)
	res, txt := callTool(t, cs, "domain_create", map[string]any{"body": map[string]any{
		"hostname": "x.exemplo.com",
		"routes":   []any{map[string]any{"path": "sem-barra", "targetType": "address", "address": "127.0.0.1:5000"}},
	}})
	require.True(t, res.IsError)
	assert.True(t, strings.HasPrefix(txt, "INVALID_PATH (HTTP 400): "), txt)

	res, txt = callTool(t, cs, "app_get", map[string]any{"id": 999})
	require.True(t, res.IsError)
	assert.True(t, strings.HasPrefix(txt, "APP_NOT_FOUND (HTTP 404): "), txt)
}

func TestMCP_GatedReplayHint(t *testing.T) {
	m := newMCPFx(t, true)
	cs := m.connect(t)
	pid, pid2 := m.project(t), m.project(t)

	res, txt := callTool(t, cs, "project_delete", map[string]any{"id": pid})
	require.True(t, res.IsError)
	assert.True(t, strings.HasPrefix(txt, "CODE_REQUIRED (HTTP 403): "), txt)
	assert.Contains(t, txt, "Peça ao usuário o código de 6 dígitos do aplicativo autenticador e chame de novo com `code`. Nunca invente nem reutilize um código.")

	*m.env.now = m.env.now.Add(30 * time.Second)
	code := m.env.code(t, m.totp)
	res, txt = callTool(t, cs, "project_delete", map[string]any{"id": pid, "code": code})
	require.False(t, res.IsError, txt)
	_, err := m.db.GetProject(context.Background(), pid)
	require.Error(t, err)

	res, txt = callTool(t, cs, "project_delete", map[string]any{"id": pid2, "code": code})
	require.True(t, res.IsError)
	assert.True(t, strings.HasPrefix(txt, "INVALID_CODE (HTTP 403): "), txt)
	assert.Contains(t, txt, "Cada código vale uma vez; se o usuário acabou de usar este, peça o próximo. Após 5 erros, os pedidos deste IP ficam bloqueados por 15 minutos.")
	_, err = m.db.GetProject(context.Background(), pid2)
	require.NoError(t, err)
}

// imageDeploy cria uma app de imagem e um deploy que fica em "building"
// (o pull do fake bloqueia).
func (m *mcpFx) imageDeploy(t *testing.T, cs *mcp.ClientSession) any {
	t.Helper()
	a, _ := okJSON(t, cs, "app_create", map[string]any{"body": imageApp(m.project(t), "espera")})
	d, _ := okJSON(t, cs, "deploy_start", map[string]any{"id": a["app"].(map[string]any)["id"]})
	return d["deploy"].(map[string]any)["id"]
}

func TestMCP_DeployWaitClampAndCancel(t *testing.T) {
	m := newMCPFx(t, false)
	cs := m.connect(t)
	did := m.imageDeploy(t, cs)

	var mu sync.Mutex
	var slept time.Duration
	restore := api.SetDeployWaitSleepForTest(func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		slept += d
		mu.Unlock()
		return ctx.Err()
	})
	w, _ := okJSON(t, cs, "deploy_wait", map[string]any{"id": did, "timeoutSeconds": 9999})
	assert.Equal(t, true, w["timedOut"])
	assert.NotContains(t, []any{"running", "failed", "superseded", "cancelled"}, w["status"])
	assert.Equal(t, 120*time.Second, slept)

	slept = 0
	okJSON(t, cs, "deploy_wait", map[string]any{"id": did})
	assert.Equal(t, 60*time.Second, slept, "padrão de 60 s")
	slept = 0
	okJSON(t, cs, "deploy_wait", map[string]any{"id": did, "timeoutSeconds": -5})
	assert.Equal(t, time.Second, slept, "mínimo de 1 s")
	restore()

	assertWaitStopsOnCancel(t, cs, did)
}

// Cliente em protocolo antigo (antes de 2026-07-28): o SDK não liga o
// cancelamento ao pedido HTTP; a desconexão ainda precisa parar deploy_wait.
func TestMCP_DeployWaitCancelOldProtocol(t *testing.T) {
	m := newMCPFx(t, false)
	cs := m.connectOpts(t, &mcp.ClientSessionOptions{ProtocolVersion: "2025-06-18"})
	require.Equal(t, "2025-06-18", cs.InitializeResult().ProtocolVersion)
	assertWaitStopsOnCancel(t, cs, m.imageDeploy(t, cs))
}

// assertWaitStopsOnCancel: com o sono real, cancelar o cliente encerra a
// chamada e a ferramenta no servidor, cada uma em até 1 s.
func assertWaitStopsOnCancel(t *testing.T, cs *mcp.ClientSession, did any) {
	t.Helper()
	started, exited := make(chan struct{}, 1), make(chan struct{}, 1)
	signal := func(c chan struct{}) {
		select {
		case c <- struct{}{}:
		default:
		}
	}
	defer api.SetDeployWaitSleepForTest(func(ctx context.Context, d time.Duration) error {
		signal(started)
		err := api.SleepCtxForTest(ctx, d)
		if err != nil {
			signal(exited)
		}
		return err
	})()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "deploy_wait",
			Arguments: map[string]any{"id": did, "timeoutSeconds": 120}})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("deploy_wait não começou a esperar")
	}
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("CallTool não voltou em 1 s depois do cancelamento")
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("deploy_wait seguiu rodando no servidor depois do cancelamento")
	}
}

func TestMCP_NoSessionOnlyTools(t *testing.T) {
	m := newMCPFx(t, true)
	tools := listTools(t, m.connect(t))
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
		assert.NotContains(t, name, "token")
		assert.NotContains(t, name, "session")
	}
	assert.False(t, slices.Contains(names, "github_manifest"), "conectar o GitHub fica no painel")
}

// O contexto de rota do chi de quem chama (o /mcp) não pode desviar o núcleo.
func TestDispatch_IgnoresCallerRouteContext(t *testing.T) {
	g := newGateFx(t)
	rctx := chi.NewRouteContext()
	rctx.RoutePath = "/mcp"
	ctx := context.WithValue(context.Background(), chi.RouteCtxKey, rctx)
	st, b := g.disp.Do(ctx, g.tok, g.user, agentAddr, http.Header{}, http.MethodGet, "/api/projects", nil, nil, "")
	assert.Equal(t, http.StatusOK, st, string(b))
}
