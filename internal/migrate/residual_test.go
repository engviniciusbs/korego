package migrate

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/docker/dockertest"
	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/store"
)

// realCaddy é um proxy.Caddy de verdade sobre Docker falso e uma Admin API
// mínima; failTrial faz o /load da config de ensaio falhar.
type realCaddy struct {
	c         *proxy.Caddy
	mu        sync.Mutex
	config    string
	failTrial bool
	owner     string // dono de 80/443 ("" = livres)
}

func newRealCaddy(t *testing.T, db *store.DB) *realCaddy {
	t.Helper()
	r := &realCaddy{config: "null"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/load":
			b, _ := io.ReadAll(req.Body)
			if r.failTrial && strings.Contains(string(b), "18443") {
				http.Error(w, "falha simulada", http.StatusInternalServerError)
				return
			}
			r.config = string(b)
		case req.Method == http.MethodGet && req.URL.Path == "/config/":
			_, _ = io.WriteString(w, r.config+"\n")
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(srv.Close)
	d := &dockertest.Fake{
		ListFn: func(context.Context) ([]docker.Container, error) {
			return []docker.Container{{ID: "c1", Name: proxy.ContainerName, State: "running", Managed: true,
				Labels: map[string]string{"korego.managed": "true", "korego.caddy-spec": "2"}}}, nil
		},
		InspectFn: func(context.Context, string) (docker.Details, error) { return docker.Details{}, nil },
	}
	r.c = proxy.NewCaddy(d, &proxy.AdminClient{BaseURL: srv.URL})
	r.c.Settings = db
	r.c.UnitFor = UnitForProcess
	r.c.PortOwner = func(p int) (string, bool) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if (p == 80 || p == 443) && r.owner != "" {
			return r.owner, true
		}
		return "", false
	}
	return r
}

func (r *realCaddy) set(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f()
}

func storedMode(t *testing.T, db *store.DB) string {
	t.Helper()
	v, _, err := db.GetSetting(context.Background(), proxy.SettingMode)
	require.NoError(t, err)
	return v
}

// revert com Switch(trial) falhando e o nginx voltando: o modo gravado fica
// trial (Switch(normal) tinha gravado normal), e uma volta com as portas
// livres segue em ensaio.
func TestRevert_TrialSwitchFailsButNginxBackStoresTrial(t *testing.T) {
	h := newHarness(t)
	// Sites que o BuildConfig real aceita (o harness usa custom sem PEM).
	h.dom.sites = []proxy.Site{site("exemplo.com", proxy.TLSAuto), site("loja.exemplo.net", proxy.TLSAuto)}
	rc := newRealCaddy(t, h.db)
	rc.set(func() { rc.owner = "nginx (pid 7)" })
	ctx := context.Background()
	require.Equal(t, proxy.StateTrial, rc.c.Ensure(ctx).State)
	h.e.Proxy = rc.c
	// O nginx parado solta as portas; de volta, as pega.
	h.sd.onChange = func(unit string, active bool) {
		rc.set(func() {
			if active {
				rc.owner = "nginx (pid 7)"
			} else {
				rc.owner = ""
			}
		})
	}
	h.verify = func(host, addr string) SiteCheck {
		if addr == NormalAddr {
			rc.set(func() { rc.failTrial = true })
			return SiteCheck{Host: host, Message: "502"}
		}
		return SiteCheck{Host: host, OK: true}
	}
	res := h.e.Takeover(ctx)
	require.Contains(t, h.c.list(), "check "+NormalAddr, "o bind real precisa ter passado")
	require.Equal(t, OutcomeFailed, res.Outcome, res.Message)
	assert.Contains(t, res.Message, "não voltou ao modo ensaio")
	assert.True(t, h.sd.isActive("nginx"))
	assert.Equal(t, "trial", storedMode(t, h.db))

	// O nginx reinicia: portas livres numa volta. O Caddy não as toma.
	rc.set(func() { rc.owner = ""; rc.failTrial = false })
	st := rc.c.Ensure(ctx)
	assert.Equal(t, proxy.StateTrial, st.State)
	assert.Equal(t, proxy.PortsFreeMessage, st.Message)
}

// Reinício do host no meio da verificação: a config do Caddy não persiste e o
// nginx (ainda ativado) pega as portas no boot. A recuperação grava trial.
func TestRecoverInterrupted_NginxActiveStoresTrial(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	require.NoError(t, h.db.SetSetting(ctx, proxy.SettingMode, "normal")) // Switch(normal) gravou
	rc := newRealCaddy(t, h.db)
	rc.set(func() { rc.owner = "nginx (pid 7)" })
	h.e.Proxy = rc.c
	setMarker(t, h, OpTakeover)
	h.dom.reconcile = func(ctx context.Context) proxy.Status { return rc.c.Ensure(ctx) }

	require.NoError(t, h.e.RecoverInterrupted(ctx))
	assert.Equal(t, "trial", storedMode(t, h.db))

	rc.set(func() { rc.owner = "" })
	st := rc.c.Ensure(ctx)
	assert.Equal(t, proxy.StateTrial, st.State)
	assert.Equal(t, proxy.PortsFreeMessage, st.Message)
}
