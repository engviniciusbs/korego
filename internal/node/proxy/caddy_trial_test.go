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
	c := newCaddy(d, admin)
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
	c := newCaddy(&dockertest.Fake{ListFn: runningOwn(specLabels)}, admin)

	// Mesmo um dono chamado "caddy" é alheio: nossa config não escuta em :443.
	c.PortOwner = func(p int) (string, bool) { return onlyWeb(p, "caddy (pid 5)") }
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
	c := newCaddy(&dockertest.Fake{ListFn: runningOwn(specLabels)}, admin)
	c.PortOwner = func(p int) (string, bool) {
		switch p {
		case 443:
			return "apache2 (pid 31)", true
		}
		return "", false
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
	c := newCaddy(d, admin)
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
	c := newCaddy(d, admin)
	c.PortOwner = func(int) (string, bool) { return "caddy (pid 99)", true }
	c.docker = withPid(d, 99)
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
	c := newCaddy(d, admin)
	c.PortOwner = freePorts
	wwwCalls := 0
	c.EnsureWWW = func() error { wwwCalls++; return nil }
	c.Ensure(context.Background())
	assert.Equal(t, []docker.BindMount{{Source: "/var/www", Target: "/var/www", ReadOnly: true}}, spec.Binds)
	assert.Equal(t, "true", spec.Labels["korego.managed"])
	assert.Equal(t, "2", spec.Labels["korego.caddy-spec"])
	assert.Equal(t, 1, wwwCalls)
}

// withPid faz o Inspect do fake devolver o PID do container.
func withPid(d *dockertest.Fake, pid int) *dockertest.Fake {
	d.InspectFn = func(context.Context, string) (docker.Details, error) {
		return docker.Details{Pid: pid}, nil
	}
	return d
}

// onlyWeb devolve o dono só para 80 e 443.
func onlyWeb(p int, owner string) (string, bool) {
	if p == 80 || p == 443 {
		return owner, true
	}
	return "", false
}

func TestEnsure_HostCaddyOwningAdminPortIsErrorAndNeverApplied(t *testing.T) {
	fa, admin := newFakeAdmin(t) // faz o papel da Admin API do Caddy do host
	fa.config = normalCfg
	d := withPid(&dockertest.Fake{ListFn: runningOwn(specLabels)}, 100)
	c := newCaddy(d, admin)
	c.PortOwner = func(int) (string, bool) { return "caddy (pid 555)", true } // 80, 443 e 2019
	c.Descends = func(pid, anc int) bool { return pid == anc }
	st := c.Ensure(context.Background())
	assert.Equal(t, StateError, st.State)
	assert.Contains(t, st.Message, "a porta 2019 da Admin API está em uso por caddy (pid 555)")
	assert.ErrorIs(t, c.Apply(context.Background(), []Site{simple("exemplo.com", "127.0.0.1:20001", false, "")}, ListenNormal, nil), ErrInactive)
	assert.Zero(t, fa.loads)
	assert.Zero(t, fa.gets, "nunca fala com uma Admin API alheia")
}

func TestEnsure_CrashLoopBecauseAdminPortTaken(t *testing.T) {
	fa, admin := newFakeAdmin(t)
	d := &dockertest.Fake{ListFn: func(context.Context) ([]docker.Container, error) {
		return []docker.Container{{ID: "c1", Name: ContainerName, State: "restarting", Managed: true, Labels: specLabels}}, nil
	}}
	c := newCaddy(d, admin)
	c.PortOwner = func(p int) (string, bool) {
		if p == 2019 {
			return "caddy (pid 555)", true
		}
		return "", false
	}
	st := c.Ensure(context.Background())
	assert.Equal(t, StateError, st.State)
	assert.Contains(t, st.Message, "2019")
	assert.Zero(t, fa.gets)
}

func TestEnsure_HostCaddyOn80And443IsTrial(t *testing.T) {
	fa, admin := newFakeAdmin(t) // nossa Admin API (2019 é do nosso container)
	fa.config = normalCfg
	d := withPid(&dockertest.Fake{ListFn: runningOwn(specLabels)}, 100)
	c := newCaddy(d, admin)
	c.PortOwner = func(p int) (string, bool) {
		switch p {
		case 2019:
			return "caddy (pid 100)", true
		default:
			return onlyWeb(p, "caddy (pid 555)")
		}
	}
	c.Descends = func(pid, anc int) bool { return pid == anc }
	st := c.Ensure(context.Background())
	assert.Equal(t, StateTrial, st.State)
	assert.Equal(t, "caddy (pid 555)", st.Process)
}

func TestEnsure_OwnerPidMatchesContainerOrDescendant(t *testing.T) {
	fa, admin := newFakeAdmin(t)
	fa.config = normalCfg
	d := withPid(&dockertest.Fake{ListFn: runningOwn(specLabels)}, 100)
	c := newCaddy(d, admin)
	c.PortOwner = func(p int) (string, bool) {
		if p == 2019 {
			return "caddy (pid 100)", true
		}
		return onlyWeb(p, "caddy (pid 101)")
	}
	c.Descends = func(pid, anc int) bool { return pid == 101 && anc == 100 }
	assert.Equal(t, StateActive, c.Ensure(context.Background()).State, "descendente do container é nosso")
	c.Descends = func(int, int) bool { return false }
	assert.Equal(t, StateTrial, c.Ensure(context.Background()).State)
}

func TestEnsure_DockerProxyIsNotWhitelisted(t *testing.T) {
	fa, admin := newFakeAdmin(t)
	fa.config = normalCfg
	c := newCaddy(withPid(&dockertest.Fake{ListFn: runningOwn(specLabels)}, 100), admin)
	c.PortOwner = func(p int) (string, bool) { return onlyWeb(p, "docker-proxy (pid 8)") }
	c.Descends = func(int, int) bool { return false }
	assert.Equal(t, StateTrial, c.Ensure(context.Background()).State)
}

func TestEnsure_NoRecreateOnAdminTimeout(t *testing.T) {
	d := &dockertest.Fake{
		ListFn: runningOwn(map[string]string{"korego.managed": "true"}),
		StopFn: func(context.Context, string) error { t.Error("stop não deveria ser chamado"); return nil },
		RemoveForceFn: func(context.Context, string) error {
			t.Error("remove não deveria ser chamado")
			return nil
		},
	}
	c := newCaddy(d, downAdmin(t))
	c.PortOwner = freePorts
	assert.Equal(t, StateStarting, c.Ensure(context.Background()).State)
}

func TestEnsure_WwwFailureAbortsRecreation(t *testing.T) {
	_, admin := newFakeAdmin(t) // config null: definitivamente não serve
	d := &dockertest.Fake{
		ListFn:   runningOwn(map[string]string{"korego.managed": "true"}),
		ImagesFn: imagePresent,
		StopFn:   func(context.Context, string) error { t.Error("stop não deveria ser chamado"); return nil },
		RemoveForceFn: func(context.Context, string) error {
			t.Error("remove não deveria ser chamado")
			return nil
		},
	}
	c := newCaddy(d, admin)
	c.PortOwner = freePorts
	c.EnsureWWW = func() error { return assert.AnError }
	assert.Equal(t, StateError, c.Ensure(context.Background()).State)
}

func TestApply_NormalRefusedInTrial(t *testing.T) {
	fa, admin := newFakeAdmin(t)
	c := newCaddy(&dockertest.Fake{ListFn: runningOwn(specLabels)}, admin)
	c.PortOwner = nginxOn80
	require.Equal(t, StateTrial, c.Ensure(context.Background()).State)
	sites := []Site{simple("exemplo.com", "127.0.0.1:20001", false, "")}
	assert.ErrorIs(t, c.Apply(context.Background(), sites, ListenNormal, nil), ErrTrialMode)
	assert.Zero(t, fa.loads)
	require.NoError(t, c.Apply(context.Background(), sites, ListenTrial, nil))
	assert.Equal(t, 1, fa.loads)
}

func TestApply_TrialRefusedWhileServingNormal(t *testing.T) {
	fa, admin := newFakeAdmin(t)
	c := newCaddy(&dockertest.Fake{ListFn: runningOwn(specLabels)}, admin)
	c.PortOwner = freePorts
	require.Equal(t, StateActive, c.Ensure(context.Background()).State)
	sites := []Site{simple("exemplo.com", "127.0.0.1:20001", false, "")}
	assert.ErrorIs(t, c.Apply(context.Background(), sites, ListenTrial, nil), ErrTrialMode)
	assert.Zero(t, fa.loads)
	require.NoError(t, c.Apply(context.Background(), sites, ListenNormal, nil))
}

func TestEnsure_InspectFailureIsStartingNotForeign(t *testing.T) {
	fa, admin := newFakeAdmin(t)
	fa.config = normalCfg
	d := &dockertest.Fake{
		ListFn: runningOwn(specLabels),
		InspectFn: func(context.Context, string) (docker.Details, error) {
			return docker.Details{}, assert.AnError
		},
	}
	c := newCaddy(d, admin)
	c.PortOwner = func(int) (string, bool) { return "caddy (pid 100)", true } // 2019, 80, 443
	st := c.Ensure(context.Background())
	assert.Equal(t, StateStarting, st.State)
	assert.Equal(t, "falha ao inspecionar o container korego-caddy", st.Message)
	assert.NotContains(t, st.Message, "2019")
	assert.ErrorIs(t, c.Apply(context.Background(), nil, ListenNormal, nil), ErrInactive)
	assert.Zero(t, fa.loads)
	assert.Zero(t, fa.gets)
}

// newCaddy cria o Caddy dos testes; sem InspectFn, o container tem PID 0.
func newCaddy(d *dockertest.Fake, a *AdminClient) *Caddy {
	if d.InspectFn == nil {
		d.InspectFn = func(context.Context, string) (docker.Details, error) { return docker.Details{}, nil }
	}
	return NewCaddy(d, a)
}

func TestSwitch_BypassesGuardAndFlipsState(t *testing.T) {
	fa, admin := newFakeAdmin(t)
	c := newCaddy(&dockertest.Fake{ListFn: runningOwn(specLabels)}, admin)
	c.PortOwner = nginxOn80
	require.Equal(t, StateTrial, c.Ensure(context.Background()).State)
	sites := []Site{simple("exemplo.com", "127.0.0.1:20001", false, "")}

	// Em ensaio, Switch(normal) carrega a config normal e passa a active/normal.
	require.NoError(t, c.Switch(context.Background(), sites, ListenNormal, nil))
	assert.Equal(t, 1, fa.loads)
	assert.True(t, listensOn443([]byte(fa.config)))
	// O guarda agora vale para o novo estado: ensaio recusado, normal aceito.
	assert.ErrorIs(t, c.Apply(context.Background(), sites, ListenTrial, nil), ErrTrialMode)
	require.NoError(t, c.Apply(context.Background(), sites, ListenNormal, nil))

	// Switch(trial) volta ao ensaio.
	require.NoError(t, c.Switch(context.Background(), sites, ListenTrial, nil))
	assert.False(t, listensOn443([]byte(fa.config)))
	assert.ErrorIs(t, c.Apply(context.Background(), sites, ListenNormal, nil), ErrTrialMode)
	require.NoError(t, c.Apply(context.Background(), sites, ListenTrial, nil))
}

func TestSwitch_LoadErrorKeepsState(t *testing.T) {
	c := newCaddy(&dockertest.Fake{ListFn: runningOwn(specLabels)}, downAdmin(t))
	c.mu.Lock()
	c.last = Status{State: StateTrial, Mode: ListenTrial}
	c.mu.Unlock()
	sites := []Site{simple("exemplo.com", "127.0.0.1:20001", false, "")}
	require.Error(t, c.Switch(context.Background(), sites, ListenNormal, nil))
	c.mu.Lock()
	defer c.mu.Unlock()
	assert.Equal(t, StateTrial, c.last.State)
}

func TestSwitch_RefusedWhenNotRunning(t *testing.T) {
	fa, admin := newFakeAdmin(t)
	c := newCaddy(&dockertest.Fake{ListFn: runningOwn(specLabels)}, admin)
	assert.ErrorIs(t, c.Switch(context.Background(), nil, ListenNormal, nil), ErrInactive)
	assert.Zero(t, fa.loads)
}
