package proxy

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/docker/dockertest"
)

// memSettings é um Settings em memória.
type memSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func newMemSettings(kv ...string) *memSettings {
	s := &memSettings{m: map[string]string{}}
	for i := 0; i+1 < len(kv); i += 2 {
		s.m[kv[i]] = kv[i+1]
	}
	return s
}

func (s *memSettings) GetSetting(_ context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	return v, ok, nil
}

func (s *memSettings) SetSetting(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
	return nil
}

func (s *memSettings) get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[key]
}

func nginxUnit(process string) (string, bool) {
	if process == "nginx (pid 7)" {
		return "nginx", true
	}
	return "", false
}

func modeCaddy(t *testing.T, set *memSettings) (*Caddy, *fakeAdmin) {
	t.Helper()
	fa, admin := newFakeAdmin(t)
	c := newCaddy(&dockertest.Fake{ListFn: runningOwn(specLabels)}, admin)
	c.Settings = set
	c.UnitFor = nginxUnit
	return c, fa
}

func TestEnsure_TrialStaysWhenPortsFree(t *testing.T) {
	set := newMemSettings()
	c, fa := modeCaddy(t, set)
	ctx := context.Background()

	c.PortOwner = nginxOn80
	st := c.Ensure(ctx)
	require.Equal(t, StateTrial, st.State)
	assert.Equal(t, "trial", set.get(SettingMode))
	assert.Equal(t, "nginx", set.get(SettingTrialUnit))

	// O nginx some por um instante (reload, boot): o Caddy não toma as portas.
	c.PortOwner = freePorts
	st = c.Ensure(ctx)
	assert.Equal(t, StateTrial, st.State)
	assert.Equal(t, ListenTrial, st.Mode)
	assert.Empty(t, st.Process)
	assert.Equal(t, PortsFreeMessage, st.Message)
	assert.Equal(t, "trial", set.get(SettingMode))
	assert.Equal(t, "nginx", set.get(SettingTrialUnit), "a unit lembrada fica")
	sites := []Site{simple("exemplo.com", "127.0.0.1:20001", false, "")}
	assert.ErrorIs(t, c.Apply(ctx, sites, ListenNormal, nil), ErrTrialMode)
	assert.Zero(t, fa.loads)
}

func TestEnsure_BootWithStoredTrialAndFreePortsIsTrial(t *testing.T) {
	set := newMemSettings(SettingMode, "trial") // config null: Caddy recém-subido
	c, _ := modeCaddy(t, set)
	c.PortOwner = freePorts
	st := c.Ensure(context.Background())
	assert.Equal(t, StateTrial, st.State)
	assert.Equal(t, ListenTrial, st.Mode)
	assert.Equal(t, PortsFreeMessage, st.Message)
}

func TestEnsure_FreshInstallStartsNormal(t *testing.T) {
	set := newMemSettings()
	c, _ := modeCaddy(t, set)
	c.PortOwner = freePorts
	st := c.Ensure(context.Background())
	assert.Equal(t, StateActive, st.State)
	assert.Equal(t, ListenNormal, st.Mode)
	assert.Equal(t, "normal", set.get(SettingMode))
	assert.Empty(t, set.get(SettingTrialUnit))
}

func TestEnsure_UpgradeServingNormalStoresNormal(t *testing.T) {
	set := newMemSettings()
	c, fa := modeCaddy(t, set)
	fa.config = normalCfg
	c.PortOwner = func(int) (string, bool) { return unknownOwner, true } // 2019, 80, 443 do container
	st := c.Ensure(context.Background())
	assert.Equal(t, StateActive, st.State)
	assert.Equal(t, "normal", set.get(SettingMode))
}

func TestEnsure_StoredTrialButServingNormalTrustsCaddy(t *testing.T) {
	// Só Switch põe o Caddy em :443; se a gravação do modo falhou depois da
	// carga, o Caddy servindo vale e o modo gravado é corrigido.
	set := newMemSettings(SettingMode, "trial")
	c, fa := modeCaddy(t, set)
	fa.config = normalCfg
	c.PortOwner = func(int) (string, bool) { return unknownOwner, true }
	assert.Equal(t, StateActive, c.Ensure(context.Background()).State)
	assert.Equal(t, "normal", set.get(SettingMode))
}

func TestEnsure_StoredNormalWithForeignOwnerKeepsStoredMode(t *testing.T) {
	set := newMemSettings(SettingMode, "normal")
	c, _ := modeCaddy(t, set)
	c.PortOwner = nginxOn80
	assert.Equal(t, StateTrial, c.Ensure(context.Background()).State)
	assert.Equal(t, "normal", set.get(SettingMode), "migrado: o nginx de volta não muda o modo gravado")
	c.PortOwner = freePorts
	assert.Equal(t, StateActive, c.Ensure(context.Background()).State)
}

func TestSwitch_PersistsMode(t *testing.T) {
	set := newMemSettings()
	c, _ := modeCaddy(t, set)
	ctx := context.Background()
	c.PortOwner = nginxOn80
	require.Equal(t, StateTrial, c.Ensure(ctx).State)
	sites := []Site{simple("exemplo.com", "127.0.0.1:20001", false, "")}
	require.NoError(t, c.Switch(ctx, sites, ListenNormal, nil))
	assert.Equal(t, "normal", set.get(SettingMode))
	require.NoError(t, c.Switch(ctx, sites, ListenTrial, nil))
	assert.Equal(t, "trial", set.get(SettingMode))
}

func TestEnsure_NoSettingsKeepsTrialInMemory(t *testing.T) {
	c, _ := modeCaddy(t, nil)
	c.Settings = nil
	c.PortOwner = nginxOn80
	require.Equal(t, StateTrial, c.Ensure(context.Background()).State)
	c.PortOwner = freePorts
	assert.Equal(t, StateTrial, c.Ensure(context.Background()).State)
}
