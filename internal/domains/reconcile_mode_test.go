package domains

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
)

// adminStub é uma Admin API mínima do Caddy (POST /load, GET /config/).
type adminStub struct {
	mu     sync.Mutex
	config string
}

func (a *adminStub) cfg() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.config
}

func newAdminStub(t *testing.T) (*adminStub, *proxy.AdminClient) {
	t.Helper()
	a := &adminStub{config: "null"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/load":
			b, _ := io.ReadAll(r.Body)
			a.config = string(b)
		case r.Method == http.MethodGet && r.URL.Path == "/config/":
			_, _ = io.WriteString(w, a.config+"\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return a, &proxy.AdminClient{BaseURL: srv.URL}
}

func TestReconcile_PortsFreeInTrialStaysTrial(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t)
	_, _, err := s.Create(ctx, addrInput("exemplo.com"))
	require.NoError(t, err)

	admin, client := newAdminStub(t)
	d := &dockertest.Fake{
		ListFn: func(context.Context) ([]docker.Container, error) {
			return []docker.Container{{ID: "c1", Name: proxy.ContainerName, State: "running", Managed: true,
				Labels: map[string]string{"korego.managed": "true", "korego.caddy-spec": "2"}}}, nil
		},
		InspectFn: func(context.Context, string) (docker.Details, error) { return docker.Details{}, nil },
	}
	c := proxy.NewCaddy(d, client)
	c.Settings = s.DB
	var mu sync.Mutex
	owner := "nginx (pid 7)"
	c.PortOwner = func(p int) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		if (p == 80 || p == 443) && owner != "" {
			return owner, true
		}
		return "", false
	}
	s.Proxy = c

	st := s.Reconcile(ctx)
	require.Equal(t, proxy.StateTrial, st.State)
	require.NotContains(t, admin.cfg(), `":443"`)

	// Volta seguinte: o nginx está fora por um instante.
	mu.Lock()
	owner = ""
	mu.Unlock()
	st = s.Reconcile(ctx)
	assert.Equal(t, proxy.StateTrial, st.State)
	assert.Equal(t, proxy.ListenTrial, st.Mode)
	assert.Equal(t, proxy.PortsFreeMessage, st.Message)
	assert.NotContains(t, admin.cfg(), `":443"`, "a config aplicada é a de ensaio")
	assert.True(t, strings.Contains(admin.cfg(), "18443"), "config de ensaio carregada")
	v, ok, err := s.DB.GetSetting(ctx, proxy.SettingMode)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "trial", v)
}
