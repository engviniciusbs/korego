package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/docker/dockertest"
)

// fakeAdmin é uma Admin API mínima: guarda o último /load e devolve em /config/.
type fakeAdmin struct {
	mu     sync.Mutex
	config string
	loads  int
}

func newFakeAdmin(t *testing.T) (*fakeAdmin, *AdminClient) {
	t.Helper()
	fa := &fakeAdmin{config: "null"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fa.mu.Lock()
		defer fa.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/load":
			b, _ := io.ReadAll(r.Body)
			fa.config = string(b)
			fa.loads++
		case r.Method == http.MethodGet && r.URL.Path == "/config/":
			_, _ = io.WriteString(w, fa.config+"\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return fa, &AdminClient{BaseURL: srv.URL}
}

// downAdmin devolve um cliente apontando para um servidor já fechado.
func downAdmin(t *testing.T) *AdminClient {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	return &AdminClient{BaseURL: srv.URL}
}

func freePorts(int) (string, bool) { return "", false }

func TestEnsure_StartsWhenPortsFree(t *testing.T) {
	_, admin := newFakeAdmin(t)
	var spec docker.RunSpec
	var pulled []string
	runs := 0
	d := &dockertest.Fake{
		ListFn: func(context.Context) ([]docker.Container, error) {
			return []docker.Container{{ID: "x", Name: "outro", State: "running"}}, nil
		},
		ImagesFn: func(context.Context, string) ([]docker.Image, error) { return nil, nil },
		PullFn: func(_ context.Context, ref string, _ func(string)) error {
			pulled = append(pulled, ref)
			return nil
		},
		RunFn: func(_ context.Context, s docker.RunSpec) (string, error) {
			runs++
			spec = s
			return "c1", nil
		},
	}
	c := NewCaddy(d, admin)
	var checked []int
	c.PortOwner = func(p int) (string, bool) { checked = append(checked, p); return "", false }

	st := c.Ensure(context.Background())
	assert.Equal(t, StateActive, st.State)
	assert.Equal(t, []int{80, 443}, checked)
	assert.Equal(t, []string{CaddyImage}, pulled)
	require.Equal(t, 1, runs)
	assert.Equal(t, ContainerName, spec.Name)
	assert.Equal(t, CaddyImage, spec.Image)
	assert.Equal(t, "host", spec.NetworkMode)
	assert.Equal(t, "unless-stopped", spec.RestartPolicy)
	assert.Equal(t, []string{"caddy", "run"}, spec.Cmd)
	assert.Equal(t, map[string]string{"korego.managed": "true"}, spec.Labels)
	assert.ElementsMatch(t, []docker.VolumeMount{
		{Volume: "korego-caddy-data", Target: "/data"},
		{Volume: "korego-caddy-config", Target: "/config"},
	}, spec.Volumes)
	assert.Empty(t, spec.Ports)
}

func TestEnsure_SkipsPullWhenImagePresent(t *testing.T) {
	_, admin := newFakeAdmin(t)
	d := &dockertest.Fake{
		ListFn: func(context.Context) ([]docker.Container, error) { return nil, nil },
		ImagesFn: func(_ context.Context, repo string) ([]docker.Image, error) {
			assert.Equal(t, "caddy", repo)
			return []docker.Image{{ID: "i1", Ref: CaddyImage}}, nil
		},
		PullFn: func(context.Context, string, func(string)) error {
			t.Error("pull não deveria ser chamado")
			return nil
		},
		RunFn: func(context.Context, docker.RunSpec) (string, error) { return "c1", nil },
	}
	c := NewCaddy(d, admin)
	c.PortOwner = freePorts
	assert.Equal(t, StateActive, c.Ensure(context.Background()).State)
}

func TestEnsure_PullFailureIsError(t *testing.T) {
	d := &dockertest.Fake{
		ListFn:   func(context.Context) ([]docker.Container, error) { return nil, nil },
		ImagesFn: func(context.Context, string) ([]docker.Image, error) { return nil, nil },
		PullFn: func(context.Context, string, func(string)) error {
			return assert.AnError
		},
	}
	c := NewCaddy(d, downAdmin(t))
	c.PortOwner = freePorts
	st := c.Ensure(context.Background())
	assert.Equal(t, StateError, st.State)
	assert.NotEmpty(t, st.Message)
}

func TestEnsure_StartingUntilAdminAnswers(t *testing.T) {
	d := &dockertest.Fake{
		ListFn: func(context.Context) ([]docker.Container, error) {
			return []docker.Container{{ID: "c1", Name: ContainerName, State: "running"}}, nil
		},
	}
	c := NewCaddy(d, downAdmin(t))
	c.PortOwner = func(int) (string, bool) { t.Error("portas não devem ser checadas"); return "", false }
	assert.Equal(t, StateStarting, c.Ensure(context.Background()).State)
}

func TestEnsure_OwnContainerHoldingPortsIsActive(t *testing.T) {
	_, admin := newFakeAdmin(t)
	d := &dockertest.Fake{
		ListFn: func(context.Context) ([]docker.Container, error) {
			return []docker.Container{{ID: "c1", Name: ContainerName, State: "running"}}, nil
		},
		RunFn: func(context.Context, docker.RunSpec) (string, error) {
			t.Error("run não deveria ser chamado")
			return "", nil
		},
	}
	c := NewCaddy(d, admin)
	// O próprio Caddy ocupa as portas: a checagem não pode acusá-lo.
	c.PortOwner = func(int) (string, bool) { return "caddy (pid 99)", true }
	st := c.Ensure(context.Background())
	assert.Equal(t, StateActive, st.State)
	assert.Empty(t, st.Process)
}

func TestEnsure_BlockedByOtherProcess(t *testing.T) {
	d := &dockertest.Fake{
		ListFn: func(context.Context) ([]docker.Container, error) { return nil, nil },
		RunFn: func(context.Context, docker.RunSpec) (string, error) {
			t.Error("run não deveria ser chamado")
			return "", nil
		},
	}
	c := NewCaddy(d, downAdmin(t))
	c.PortOwner = func(p int) (string, bool) {
		if p == 80 {
			return "nginx (pid 7)", true
		}
		return "", false
	}
	st := c.Ensure(context.Background())
	assert.Equal(t, StateBlocked, st.State)
	assert.Equal(t, "nginx (pid 7)", st.Process)
	assert.Contains(t, st.Message, "80")
}

func TestEnsure_RestartsStopped(t *testing.T) {
	_, admin := newFakeAdmin(t)
	var started []string
	d := &dockertest.Fake{
		ListFn: func(context.Context) ([]docker.Container, error) {
			return []docker.Container{{ID: "c1", Name: ContainerName, State: "exited"}}, nil
		},
		StartFn: func(_ context.Context, id string) error { started = append(started, id); return nil },
	}
	c := NewCaddy(d, admin)
	c.PortOwner = freePorts
	assert.Equal(t, StateActive, c.Ensure(context.Background()).State)
	assert.Equal(t, []string{"c1"}, started)
}

func TestEnsure_DockerDown(t *testing.T) {
	c := NewCaddy(&dockertest.Fake{}, downAdmin(t)) // ListFn nil → ErrUnavailable
	c.PortOwner = freePorts
	st := c.Ensure(context.Background())
	assert.Equal(t, StateDockerUnavailable, st.State)
	assert.NotEmpty(t, st.Message)
}

func TestApply_InactiveError(t *testing.T) {
	fa, admin := newFakeAdmin(t)
	d := &dockertest.Fake{
		ListFn: func(context.Context) ([]docker.Container, error) {
			return []docker.Container{{ID: "c1", Name: ContainerName, State: "running"}}, nil
		},
	}
	c := NewCaddy(d, admin)
	routes := []Route{{Host: "exemplo.com", Upstream: "127.0.0.1:20001"}}

	// Sem Ensure ainda: inativo.
	assert.ErrorIs(t, c.Apply(context.Background(), routes), ErrInactive)
	assert.Equal(t, 0, fa.loads)

	require.Equal(t, StateActive, c.Ensure(context.Background()).State)
	require.NoError(t, c.Apply(context.Background(), routes))
	assert.Equal(t, 1, fa.loads)
	want, err := BuildConfig(routes)
	require.NoError(t, err)
	assert.True(t, SameConfig(want, []byte(fa.config)))

	// Rota inválida: erro do BuildConfig, nada enviado.
	assert.Error(t, c.Apply(context.Background(), []Route{{Host: "exemplo.com"}}))
	assert.Equal(t, 1, fa.loads)

	// Docker caiu: o último estado deixa de ser active.
	d.ListFn = nil
	require.Equal(t, StateDockerUnavailable, c.Ensure(context.Background()).State)
	assert.ErrorIs(t, c.Apply(context.Background(), routes), ErrInactive)
}

func TestInSync_DetectsEmptyAfterRestart(t *testing.T) {
	fa, admin := newFakeAdmin(t)
	c := NewCaddy(&dockertest.Fake{}, admin)
	routes := []Route{{Host: "exemplo.com", Upstream: "127.0.0.1:20001", ForceHTTPS: true}}

	ok, err := c.InSync(context.Background(), routes)
	require.NoError(t, err)
	assert.False(t, ok, "config null não está em sincronia")

	cfg, err := BuildConfig(routes)
	require.NoError(t, err)
	fa.mu.Lock()
	fa.config = string(cfg)
	fa.mu.Unlock()
	ok, err = c.InSync(context.Background(), routes)
	require.NoError(t, err)
	assert.True(t, ok)

	_, err = NewCaddy(&dockertest.Fake{}, downAdmin(t)).InSync(context.Background(), routes)
	assert.Error(t, err)
}
