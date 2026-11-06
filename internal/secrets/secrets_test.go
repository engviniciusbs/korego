package secrets_test

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/engviniciusbs/korego/internal/secrets"
	"github.com/stretchr/testify/require"
)

func newBox(t *testing.T) *secrets.Box {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	b, err := secrets.NewBox(key)
	require.NoError(t, err)
	return b
}

func TestKey_CreateThenReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "master.key")
	k1, err := secrets.LoadOrCreateKey(path, true)
	require.NoError(t, err)
	require.Len(t, k1, 32)
	k2, err := secrets.LoadOrCreateKey(path, false)
	require.NoError(t, err)
	require.Equal(t, k1, k2)
}

func TestKey_Permissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permissões POSIX não se aplicam ao Windows")
	}
	path := filepath.Join(t.TempDir(), "master.key")
	_, err := secrets.LoadOrCreateKey(path, true)
	require.NoError(t, err)
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
}

func TestKey_MissingNotAllowed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	_, err := secrets.LoadOrCreateKey(path, false)
	require.True(t, errors.Is(err, secrets.ErrKeyMissing))
	_, statErr := os.Stat(path)
	require.True(t, os.IsNotExist(statErr))
}

func TestKey_WrongSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	require.NoError(t, os.WriteFile(path, []byte("curta"), 0o600))
	_, err := secrets.LoadOrCreateKey(path, true)
	require.Error(t, err)
	require.False(t, errors.Is(err, secrets.ErrKeyMissing))
}

func TestBox_RoundTrip(t *testing.T) {
	b := newBox(t)
	enc, err := b.Encrypt([]byte("segredo"))
	require.NoError(t, err)
	dec, err := b.Decrypt(enc)
	require.NoError(t, err)
	require.Equal(t, []byte("segredo"), dec)
}

func TestBox_TamperFails(t *testing.T) {
	b := newBox(t)
	enc, err := b.Encrypt([]byte("segredo"))
	require.NoError(t, err)
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(enc)
	}
	require.NoError(t, err)
	raw[len(raw)-1] ^= 0x01
	_, err = b.Decrypt(base64.RawURLEncoding.EncodeToString(raw))
	require.Error(t, err)
}

func TestBox_WrongKeyFails(t *testing.T) {
	enc, err := newBox(t).Encrypt([]byte("segredo"))
	require.NoError(t, err)
	other, err := secrets.NewBox(make([]byte, 32))
	require.NoError(t, err)
	_, err = other.Decrypt(enc)
	require.Error(t, err)
}

func TestBox_NonceUnique(t *testing.T) {
	b := newBox(t)
	a, err := b.Encrypt([]byte("igual"))
	require.NoError(t, err)
	c, err := b.Encrypt([]byte("igual"))
	require.NoError(t, err)
	require.NotEqual(t, a, c)
}

func TestBox_MalformedInput(t *testing.T) {
	b := newBox(t)
	for _, in := range []string{"", "!!!não-base64!!!", "AAAA"} {
		require.NotPanics(t, func() {
			_, err := b.Decrypt(in)
			require.Error(t, err)
		})
	}
}

func TestNewBox_BadKeySize(t *testing.T) {
	_, err := secrets.NewBox([]byte("curta"))
	require.Error(t, err)
}
