package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/engviniciusbs/korego/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_Defaults(t *testing.T) {
	for _, k := range []string{"KOREGO_LISTEN", "KOREGO_DATA_DIR", "KOREGO_CONFIG_DIR", "KOREGO_DEV_PROXY"} {
		t.Setenv(k, "")
	}
	c, err := config.Load(filepath.Join(t.TempDir(), "nao-existe.toml"))
	require.NoError(t, err)
	assert.Equal(t, ":8000", c.ListenAddr)
	assert.Equal(t, "/var/lib/korego", c.DataDir)
	assert.Equal(t, "/etc/korego", c.ConfigDir)
	assert.Equal(t, "", c.DevProxy)
}

func TestLoad_FileThenEnvOverride(t *testing.T) {
	t.Setenv("KOREGO_LISTEN", "")
	t.Setenv("KOREGO_DATA_DIR", "")
	t.Setenv("KOREGO_CONFIG_DIR", "")
	t.Setenv("KOREGO_DEV_PROXY", "")
	p := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(p, []byte("listen = \":9000\"\ndata_dir = \"/d\"\ndev_proxy = \"http://localhost:3000\"\n"), 0o600))

	c, err := config.Load(p)
	require.NoError(t, err)
	assert.Equal(t, ":9000", c.ListenAddr)
	assert.Equal(t, "/d", c.DataDir)
	assert.Equal(t, "/etc/korego", c.ConfigDir)
	assert.Equal(t, "http://localhost:3000", c.DevProxy)

	t.Setenv("KOREGO_LISTEN", ":9100")
	c, err = config.Load(p)
	require.NoError(t, err)
	assert.Equal(t, ":9100", c.ListenAddr)
}

func TestLoad_InvalidFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(p, []byte("listen = ["), 0o600))
	_, err := config.Load(p)
	assert.Error(t, err)
}
