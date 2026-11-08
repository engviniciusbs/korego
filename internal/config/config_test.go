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
	for _, k := range []string{"KOREGO_LISTEN", "KOREGO_DATA_DIR", "KOREGO_CONFIG_DIR", "KOREGO_DEV_PROXY", "KOREGO_PM2_PATH", "KOREGO_PM2_HOME"} {
		t.Setenv(k, "")
	}
	c, err := config.Load(filepath.Join(t.TempDir(), "nao-existe.toml"))
	require.NoError(t, err)
	assert.Equal(t, ":8000", c.ListenAddr)
	assert.Equal(t, "/var/lib/korego", c.DataDir)
	assert.Equal(t, "/etc/korego", c.ConfigDir)
	assert.Equal(t, "", c.DevProxy)
	assert.Equal(t, "", c.PM2Path)
	assert.Equal(t, "", c.PM2Home)
}

func TestLoad_FileThenEnvOverride(t *testing.T) {
	t.Setenv("KOREGO_LISTEN", "")
	t.Setenv("KOREGO_DATA_DIR", "")
	t.Setenv("KOREGO_CONFIG_DIR", "")
	t.Setenv("KOREGO_DEV_PROXY", "")
	t.Setenv("KOREGO_PM2_PATH", "")
	t.Setenv("KOREGO_PM2_HOME", "")
	p := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(p, []byte("listen = \":9000\"\ndata_dir = \"/d\"\ndev_proxy = \"http://localhost:3000\"\npm2_path = \"/opt/pm2\"\npm2_home = \"/home/deploy/.pm2\"\n"), 0o600))

	c, err := config.Load(p)
	require.NoError(t, err)
	assert.Equal(t, ":9000", c.ListenAddr)
	assert.Equal(t, "/d", c.DataDir)
	assert.Equal(t, "/etc/korego", c.ConfigDir)
	assert.Equal(t, "http://localhost:3000", c.DevProxy)

	assert.Equal(t, "/opt/pm2", c.PM2Path)
	assert.Equal(t, "/home/deploy/.pm2", c.PM2Home)

	t.Setenv("KOREGO_LISTEN", ":9100")
	t.Setenv("KOREGO_PM2_PATH", "/env/pm2")
	t.Setenv("KOREGO_PM2_HOME", "/env/.pm2")
	c, err = config.Load(p)
	require.NoError(t, err)
	assert.Equal(t, ":9100", c.ListenAddr)
	assert.Equal(t, "/env/pm2", c.PM2Path)
	assert.Equal(t, "/env/.pm2", c.PM2Home)
}

func TestLoad_InvalidFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(p, []byte("listen = ["), 0o600))
	_, err := config.Load(p)
	assert.Error(t, err)
}

func TestLoad_PublicIP(t *testing.T) {
	t.Setenv("KOREGO_PUBLIC_IP", "")
	c, err := config.Load(filepath.Join(t.TempDir(), "nao-existe.toml"))
	require.NoError(t, err)
	assert.Equal(t, "", c.PublicIP)

	p := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(p, []byte("public_ip = \"203.0.113.10\"\n"), 0o600))
	c, err = config.Load(p)
	require.NoError(t, err)
	assert.Equal(t, "203.0.113.10", c.PublicIP)

	t.Setenv("KOREGO_PUBLIC_IP", "203.0.113.20")
	c, err = config.Load(p)
	require.NoError(t, err)
	assert.Equal(t, "203.0.113.20", c.PublicIP)
}

func TestLoad_MaxBuilds(t *testing.T) {
	t.Setenv("KOREGO_MAX_BUILDS", "")
	c, err := config.Load(filepath.Join(t.TempDir(), "nao-existe.toml"))
	require.NoError(t, err)
	assert.Equal(t, 2, c.MaxBuilds)

	p := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(p, []byte("max_builds = 4\n"), 0o600))
	c, err = config.Load(p)
	require.NoError(t, err)
	assert.Equal(t, 4, c.MaxBuilds)

	t.Setenv("KOREGO_MAX_BUILDS", "3")
	c, err = config.Load(p)
	require.NoError(t, err)
	assert.Equal(t, 3, c.MaxBuilds)

	t.Setenv("KOREGO_MAX_BUILDS", "muitos")
	_, err = config.Load(p)
	assert.Error(t, err)
}
