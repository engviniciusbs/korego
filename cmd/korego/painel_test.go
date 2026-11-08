package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/engviniciusbs/korego/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tempConfig grava um config.toml com data_dir num diretório temporário.
func tempConfig(t *testing.T) (cfgPath, dataDir string) {
	t.Helper()
	dir := t.TempDir()
	dataDir = filepath.Join(dir, "data")
	require.NoError(t, os.MkdirAll(dataDir, 0o750))
	cfgPath = filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(cfgPath,
		[]byte("data_dir = \""+filepath.ToSlash(dataDir)+"\"\n"), 0o600))
	t.Setenv("KOREGO_DATA_DIR", "")
	return cfgPath, dataDir
}

func TestPainelCommand_ReenablesPublicPort(t *testing.T) {
	cfgPath, dataDir := tempConfig(t)
	ctx := context.Background()
	db, err := store.Open(dbPath(dataDir))
	require.NoError(t, err)
	require.NoError(t, db.SetSetting(ctx, "panel.public_port", "false"))
	require.NoError(t, db.Close())

	var out, errOut bytes.Buffer
	code := runPainel([]string{"--porta-publica", "--config", cfgPath}, &out, &errOut)
	require.Equal(t, 0, code, errOut.String())
	assert.Equal(t, "Porta pública reativada. Reinicie: systemctl restart korego\n", out.String())

	db, err = store.Open(dbPath(dataDir))
	require.NoError(t, err)
	defer db.Close()
	v, ok, err := db.GetSetting(ctx, "panel.public_port")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "true", v)
}

func TestPainelCommand_UsageWithoutFlag(t *testing.T) {
	cfgPath, _ := tempConfig(t)
	var out, errOut bytes.Buffer
	assert.Equal(t, 2, runPainel([]string{"--config", cfgPath}, &out, &errOut))
	assert.Contains(t, errOut.String(), "--porta-publica")
	assert.Empty(t, out.String())
}

func TestPainelCommand_MissingDatabase(t *testing.T) {
	cfgPath, dataDir := tempConfig(t)
	var out, errOut bytes.Buffer
	assert.Equal(t, 1, runPainel([]string{"--porta-publica", "--config", cfgPath}, &out, &errOut))
	assert.Contains(t, errOut.String(), "banco")
	_, err := os.Stat(dbPath(dataDir))
	assert.ErrorIs(t, err, os.ErrNotExist, "não cria banco novo")
}

func TestHealthURLs(t *testing.T) {
	got, err := healthURLs(":8000")
	require.NoError(t, err)
	assert.Equal(t, []string{"http://127.0.0.1:8000/api/health"}, got)

	got, err = healthURLs("203.0.113.10:8000")
	require.NoError(t, err)
	assert.Equal(t, []string{"http://203.0.113.10:8000/api/health", "http://127.0.0.1:8000/api/health"}, got)
}
