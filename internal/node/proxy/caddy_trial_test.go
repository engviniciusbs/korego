package proxy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/docker/dockertest"
)

// specLabels são os labels de um korego-caddy criado pela versão atual.
var specLabels = map[string]string{"korego.managed": "true", "korego.caddy-spec": "2"}

// normalCfg é uma config que escuta em :443 (Caddy servindo em modo normal).
const normalCfg = `{"apps":{"http":{"servers":{"s":{"listen":[":443"]}}}}}`

func runningOwn(labels map[string]string) func(context.Context) ([]docker.Container, error) {
	return func(context.Context) ([]docker.Container, error) {
		return []docker.Container{{ID: "c1", Name: ContainerName, State: "running", Managed: true, Labels: labels}}, nil
	}
}

func imagePresent(context.Context, string) ([]docker.Image, error) {
	return []docker.Image{{Ref: CaddyImage}}, nil
}

func TestEnsure_ForeignPortsGivesTrial(t *testing.T) {
	// Sem container: cria e roda mesmo com 80 ocupada; estado trial.
	_, admin := newFakeAdmin(t)
	runs := 0
	d := &dockertest.Fake{
		ListFn:   func(context.Context) ([]docker.Container, error) { return nil, nil },
		ImagesFn: imagePresent,
		RunFn:    func(context.Context, docker.RunSpec) (string, error) { runs++; return "c1", nil },
	}
	c := NewCaddy(d, admin)
	c.EnsureWWW = func() error { return nil }
	c.PortOwner = nginxOn80
	st := c.Ensure(context.Background())
	assert.Equal(t, StateTrial, st.State)
	assert.Equal(t, ListenTrial, st.Mode)
	assert.Equal(t, "nginx (pid 7)", st.Process)
	assert.Equal(t, 1, runs)
	// Trial permite Apply.
	require.NoError(t, c.Apply(context.Background(), []Site{simple("exemplo.com", "127.0.0.1:20001", false, "")}, ListenTrial, nil))

	// Container parado: é iniciado, não bloqueado.
	var started []string
	d.ListFn = func(context.Context) ([]docker.Container, error) {
		return []docker.Container{{ID: "c1", Name: ContainerName, State: "exited", Managed: true, Labels: specLabels}}, nil
	}
	d.StartFn = func(_ context.Context, id string) error { started = append(started, id); return nil }
	assert.Equal(t, StateTrial, c.Ensure(context.Background()).State)
	assert.Equal(t, []string{"c1"}, started)
}

func TestEnsure_TrialConfigStillForeign(t *testing.T) {
	fa, admin := newFakeAdmin(t)
	cfg, err := BuildConfig([]Site{simple("exemplo.com", "127.0.0.1:20001", false, "")}, ListenTrial, nil)
	require.NoError(t, err)
	fa.config = string(cfg) // Caddy em 18080/18443, não em :443
	c := NewCaddy(&dockertest.Fake{ListFn: runningOwn(specLabels)}, admin)

	// Mesmo um dono chamado "caddy" é alheio: nossa config não escuta em :443.
	c.PortOwner = func(int) (string, bool) { return "caddy (pid 5)", true }
	st := c.Ensure(context.Background())
	assert.Equal(t, StateTrial, st.State)
	assert.Equal(t, "caddy (pid 5)", st.Process)

	// Portas livres com config de ensaio: pronto para o modo normal.
	c.PortOwner = freePorts
	st = c.Ensure(context.Background())
	assert.Equal(t, StateActive, st.State)
	assert.Equal(t, ListenNormal, st.Mode)
}

func TestEnsure_NormalConfigWithForeignOwnerIsTrial(t *testing.T) {
	fa, admin := newFakeAdmin(t)
	fa.config = normalCfg
	c := NewCaddy(&dockertest.Fake{ListFn: runningOwn(specLabels)}, admin)
	c.PortOwner = func(p int) (string, bool) {
		if p == 443 {
			return "apache2 (pid 31)", true
		}
		return "docker-proxy (pid 3)", true
	}
	st := c.Ensure(context.Background())
	assert.Equal(t, StateTrial, st.State)
	assert.Equal(t, "apache2 (pid 31)", st.Process)
}

func TestEnsure_RecreatesOldSpecWhenNotServing(t *testing.T) {
	_, admin := newFakeAdmin(t) // config null: não serve
	var calls []string
	var spec docker.RunSpec
	d := &dockertest.Fake{
		ListFn:   runningOwn(map[string]string{"korego.managed": "true"}), // sem label de spec
		ImagesFn: imagePresent,
		StopFn:   func(_ context.Context, id string) error { calls = append(calls, "stop:"+id); return nil },
		RemoveForceFn: func(_ context.Context, id string) error {
			calls = append(calls, "rm:"+id)
			return nil
		},
		RunFn: func(_ context.Context, s docker.RunSpec) (string, error) {
			calls = append(calls, "run")
			spec = s
			return "c2", nil
		},
	}
	c := NewCaddy(d, admin)
	c.EnsureWWW = func() error { return nil }
	c.PortOwner = freePorts
	st := c.Ensure(context.Background())
	assert.Equal(t, StateActive, st.State)
	assert.Equal(t, []string{"stop:c1", "rm:c1", "run"}, calls)
	assert.Equal(t, "2", spec.Labels["korego.caddy-spec"])
}

func TestEnsure_DoesNotRecreateWhileServingNormal(t *testing.T) {
	fa, admin := newFakeAdmin(t)
	fa.config = normalCfg
	d := &dockertest.Fake{
		ListFn: runningOwn(map[string]string{"korego.managed": "true", "korego.caddy-spec": "1"}),
		StopFn: func(context.Context, string) error { t.Error("stop não deveria ser chamado"); return nil },
		RemoveForceFn: func(context.Context, string) error {
			t.Error("remove não deveria ser chamado")
			return nil
		},
		RunFn: func(context.Context, docker.RunSpec) (string, error) {
			t.Error("run não deveria ser chamado")
			return "", nil
		},
	}
	c := NewCaddy(d, admin)
	c.PortOwner = func(int) (string, bool) { return "caddy (pid 99)", true }
	st := c.Ensure(context.Background())
	assert.Equal(t, StateActive, st.State, "recriação pendente, segue servindo")
	assert.Equal(t, ListenNormal, st.Mode)
}

func TestEnsure_RunSpecHasWwwBindAndLabel(t *testing.T) {
	_, admin := newFakeAdmin(t)
	var spec docker.RunSpec
	d := &dockertest.Fake{
		ListFn:   func(context.Context) ([]docker.Container, error) { return nil, nil },
		ImagesFn: imagePresent,
		RunFn:    func(_ context.Context, s docker.RunSpec) (string, error) { spec = s; return "c1", nil },
	}
	c := NewCaddy(d, admin)
	c.PortOwner = freePorts
	wwwCalls := 0
	c.EnsureWWW = func() error { wwwCalls++; return nil }
	c.Ensure(context.Background())
	assert.Equal(t, []docker.BindMount{{Source: "/var/www", Target: "/var/www", ReadOnly: true}}, spec.Binds)
	assert.Equal(t, "true", spec.Labels["korego.managed"])
	assert.Equal(t, "2", spec.Labels["korego.caddy-spec"])
	assert.Equal(t, 1, wwwCalls)
}
