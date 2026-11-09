package api_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/domains"
	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/migrate"
	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/node/proxy/proxytest"
	"github.com/engviniciusbs/korego/internal/store"
)

// fakeUnits é um ServiceManager em memória para os testes da API.
type fakeUnits struct {
	mu     sync.Mutex
	active map[string]bool
}

func (f *fakeUnits) set(unit string, v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active[unit] = v
}
func (f *fakeUnits) isActive(unit string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active[unit]
}
func (f *fakeUnits) Stop(_ context.Context, u string) error  { f.set(u, false); return nil }
func (f *fakeUnits) Start(_ context.Context, u string) error { f.set(u, true); return nil }
func (f *fakeUnits) Enable(context.Context, string) error    { return nil }
func (f *fakeUnits) Disable(context.Context, string) error   { return nil }
func (f *fakeUnits) Active(_ context.Context, u string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active[u], nil
}
func (f *fakeUnits) Exists(context.Context, string) bool { return true }
func (f *fakeUnits) UnitForProcess(p string) (string, bool) {
	return migrate.UnitForProcess(p)
}

type migEnv struct {
	e      *env
	svc    *domains.Service
	px     *proxytest.Fake
	units  *fakeUnits
	mig    *migrate.Engine
	secret string
	cookie *http.Cookie
	csrf   string
	owner  string // dono das portas visto por Ensure enquanto o nginx roda
	mu     sync.Mutex
}

func newMigEnv(t *testing.T) *migEnv {
	t.Helper()
	m := &migEnv{units: &fakeUnits{active: map[string]bool{"nginx": true}}, owner: "nginx (pid 7)"}
	m.px = &proxytest.Fake{}
	m.px.EnsureFn = func(context.Context) proxy.Status {
		m.mu.Lock()
		defer m.mu.Unlock()
		if a, _ := m.units.Active(context.Background(), "nginx"); a {
			return proxy.Status{State: proxy.StateTrial, Mode: proxy.ListenTrial, Process: m.owner}
		}
		return proxy.Status{State: proxy.StateActive, Mode: proxy.ListenNormal}
	}
	db := store.OpenMemory(t)
	m.svc = &domains.Service{DB: db, Proxy: m.px, Hub: events.NewHub(), PanelPort: 8000, ListenAddr: ":8000"}
	m.mig = &migrate.Engine{
		Domains: m.svc, Proxy: m.px, Systemd: m.units, DB: db, Hub: m.svc.Hub,
		Check: func(_ context.Context, sites []proxy.Site, _ string, _ bool) []migrate.SiteCheck {
			out := []migrate.SiteCheck{}
			for _, s := range sites {
				out = append(out, migrate.SiteCheck{Host: s.Host, OK: true})
			}
			return out
		},
	}
	m.e = newEnvDeps(t, api.Deps{Domains: m.svc, Migrate: m.mig, Hub: m.svc.Hub})
	m.secret, m.cookie, m.csrf = m.e.setup(t)
	m.svc.Reconcile(context.Background())
	return m
}

// freshCode avança o relógio um passo e gera um código TOTP novo.
func (m *migEnv) freshCode(t *testing.T) string {
	*m.e.now = m.e.now.Add(30 * time.Second)
	return m.e.code(t, m.secret)
}

func (m *migEnv) post(t *testing.T, path string, body any) map[string]any {
	t.Helper()
	w := m.e.do(t, http.MethodPost, path, body, mut(m.cookie, m.csrf)...)
	out := decode(t, w)
	out["_status"] = float64(w.Code)
	return out
}

func (m *migEnv) waitIdle(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool { return !m.mig.Running() }, 5*time.Second, 5*time.Millisecond)
}

func (m *migEnv) auditActions(t *testing.T, action string) []map[string]any {
	t.Helper()
	w := m.e.do(t, http.MethodGet, "/api/audit?limit=100", nil, withCookie(m.cookie))
	require.Equal(t, http.StatusOK, w.Code)
	var out []map[string]any
	for _, it := range decode(t, w)["items"].([]any) {
		e := it.(map[string]any)
		if e["action"] == action {
			out = append(out, e["metadata"].(map[string]any))
		}
	}
	return out
}

func TestAPI_TakeoverRequiresCode(t *testing.T) {
	m := newMigEnv(t)

	b := m.post(t, "/api/proxy/takeover", nil)
	assert.EqualValues(t, http.StatusForbidden, b["_status"])
	assert.Equal(t, "CODE_REQUIRED", errCode(b))
	b = m.post(t, "/api/proxy/takeover", map[string]string{"code": " "})
	assert.Equal(t, "CODE_REQUIRED", errCode(b))

	b = m.post(t, "/api/proxy/takeover", map[string]string{"code": "000000"})
	assert.EqualValues(t, http.StatusForbidden, b["_status"])
	assert.Equal(t, "INVALID_CODE", errCode(b))
	b = m.post(t, "/api/proxy/rollback", map[string]string{"code": "000000"})
	assert.Equal(t, "INVALID_CODE", errCode(b))
	assert.False(t, m.mig.Running())
	assert.True(t, m.units.isActive("nginx"))

	// Código errado é auditado.
	audits := m.auditActions(t, "proxy.takeover")
	require.Len(t, audits, 1)
	assert.Equal(t, false, audits[0]["ok"])
	assert.Equal(t, "invalid_code", audits[0]["stage"])

	// Sem CSRF: recusado antes.
	w := m.e.do(t, http.MethodPost, "/api/proxy/takeover", map[string]string{"code": m.freshCode(t)}, withCookie(m.cookie))
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.NotContains(t, w.Body.String(), "INVALID_CODE")
}

func TestAPI_TakeoverAndRollback(t *testing.T) {
	m := newMigEnv(t)
	ch, cancel := m.svc.Hub.Subscribe(migrate.TopicMigration)
	defer cancel()

	b := m.post(t, "/api/proxy/takeover", map[string]string{"code": m.freshCode(t)})
	require.EqualValues(t, http.StatusAccepted, b["_status"], b)
	assert.Equal(t, migrate.TopicMigration, b["topic"])
	m.waitIdle(t)

	var last migrate.Event
	for len(ch) > 0 {
		last = (<-ch).Data.(migrate.Event)
	}
	assert.True(t, last.Done)
	assert.Equal(t, migrate.OutcomeOK, last.Result)

	w := m.e.do(t, http.MethodGet, "/api/proxy", nil, withCookie(m.cookie))
	require.Equal(t, http.StatusOK, w.Code)
	st := decode(t, w)
	assert.Equal(t, "active", st["state"])
	assert.Equal(t, "normal", st["mode"])
	assert.Equal(t, "nginx", st["migratedFrom"])
	assert.NotNil(t, st["migratedAt"])
	assert.Equal(t, true, st["rollbackAvailable"])
	assert.Equal(t, false, st["migrating"])
	lm := st["lastMigration"].(map[string]any)
	assert.Equal(t, "takeover", lm["op"])
	assert.Equal(t, "ok", lm["outcome"])

	// Fora do ensaio: o ensaio e um novo assumir dão 409.
	b = m.post(t, "/api/proxy/takeover", map[string]string{"code": m.freshCode(t)})
	assert.EqualValues(t, http.StatusConflict, b["_status"])
	assert.Equal(t, "PROXY_NOT_TRIAL", errCode(b))
	w = m.e.do(t, http.MethodGet, "/api/proxy/trial-check", nil, withCookie(m.cookie))
	assert.Equal(t, http.StatusConflict, w.Code)

	b = m.post(t, "/api/proxy/rollback", map[string]string{"code": m.freshCode(t)})
	require.EqualValues(t, http.StatusAccepted, b["_status"], b)
	m.waitIdle(t)
	assert.True(t, m.units.isActive("nginx"))
	assert.Equal(t, proxy.ListenTrial, m.px.LastMode())

	st = decode(t, m.e.do(t, http.MethodGet, "/api/proxy", nil, withCookie(m.cookie)))
	assert.Equal(t, "trial", st["state"])
	assert.Nil(t, st["migratedFrom"])
	assert.Equal(t, false, st["rollbackAvailable"])
	assert.Equal(t, "rollback", st["lastMigration"].(map[string]any)["op"])

	// Sem migração registrada: 409.
	b = m.post(t, "/api/proxy/rollback", map[string]string{"code": m.freshCode(t)})
	assert.EqualValues(t, http.StatusConflict, b["_status"])
	assert.Equal(t, "PROXY_NOT_MIGRATED", errCode(b))

	// Auditoria: início e resultado de cada troca.
	stages := map[string]bool{}
	for _, a := range m.auditActions(t, "proxy.takeover") {
		stages[a["stage"].(string)] = true
	}
	assert.True(t, stages["started"])
	assert.True(t, stages["done"])
	assert.True(t, stages["refused"])
	assert.NotEmpty(t, m.auditActions(t, "proxy.rollback"))
}

func TestAPI_TakeoverUnsupportedOwner409(t *testing.T) {
	m := newMigEnv(t)
	m.mu.Lock()
	m.owner = "haproxy (pid 9)"
	m.mu.Unlock()
	b := m.post(t, "/api/proxy/takeover", map[string]string{"code": m.freshCode(t)})
	assert.EqualValues(t, http.StatusConflict, b["_status"])
	assert.Equal(t, "UNSUPPORTED_PORT_OWNER", errCode(b))
	assert.Contains(t, b["error"].(map[string]any)["message"], "haproxy")
	assert.True(t, m.units.isActive("nginx"))
	assert.False(t, m.svc.Paused())
}

func TestAPI_TrialCheck(t *testing.T) {
	m := newMigEnv(t)
	_, _, err := m.svc.Create(context.Background(), domains.DomainInput{
		Hostname: "exemplo.com", TargetType: "address", Address: "127.0.0.1:5000"})
	require.NoError(t, err)
	w := m.e.do(t, http.MethodGet, "/api/proxy/trial-check", nil, withCookie(m.cookie))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	sites := decode(t, w)["sites"].([]any)
	require.Len(t, sites, 1)
	assert.Equal(t, "exemplo.com", sites[0].(map[string]any)["host"])
	assert.Equal(t, true, sites[0].(map[string]any)["ok"])
}
