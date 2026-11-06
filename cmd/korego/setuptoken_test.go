package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadOrCreateSetupToken_CreatesWith0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "setup-token")
	tok, err := loadOrCreateSetupToken(path)
	require.NoError(t, err)
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	require.NoError(t, err)
	require.Len(t, raw, 32)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, tok, string(data))

	if runtime.GOOS == "windows" {
		t.Skip("permissões de arquivo não se aplicam no Windows")
	}
	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}

func TestLoadOrCreateSetupToken_ReusesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "setup-token")
	first, err := loadOrCreateSetupToken(path)
	require.NoError(t, err)
	second, err := loadOrCreateSetupToken(path)
	require.NoError(t, err)
	require.Equal(t, first, second)

	require.NoError(t, os.WriteFile(path, []byte("token-existente\n"), 0o600))
	got, err := loadOrCreateSetupToken(path)
	require.NoError(t, err)
	require.Equal(t, "token-existente", got)
}

func TestLoadOrCreateSetupToken_EmptyFileIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "setup-token")
	require.NoError(t, os.WriteFile(path, []byte("  \n"), 0o600))
	_, err := loadOrCreateSetupToken(path)
	require.Error(t, err)
}
