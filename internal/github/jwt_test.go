package github

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return k
}

func pemPKCS1(k *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
}

func TestAppJWT_VerifiesWithPublicKey(t *testing.T) {
	k := testKey(t)
	now := time.Unix(1700000000, 0)
	tok, err := AppJWT(12345, k, now)
	require.NoError(t, err)

	parts := strings.Split(tok, ".")
	require.Len(t, parts, 3)
	dec := func(s string) []byte {
		b, err := base64.RawURLEncoding.DecodeString(s)
		require.NoError(t, err)
		return b
	}
	assert.JSONEq(t, `{"alg":"RS256","typ":"JWT"}`, string(dec(parts[0])))
	var claims map[string]any
	require.NoError(t, json.Unmarshal(dec(parts[1]), &claims))
	assert.EqualValues(t, now.Unix()-60, claims["iat"])
	assert.EqualValues(t, now.Unix()+540, claims["exp"])
	assert.Equal(t, "12345", claims["iss"])

	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	require.NoError(t, rsa.VerifyPKCS1v15(&k.PublicKey, crypto.SHA256, sum[:], dec(parts[2])))
}

func TestParsePrivateKey(t *testing.T) {
	k := testKey(t)
	got, err := ParsePrivateKey(pemPKCS1(k))
	require.NoError(t, err)
	assert.True(t, got.Equal(k))

	der, err := x509.MarshalPKCS8PrivateKey(k)
	require.NoError(t, err)
	got, err = ParsePrivateKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	require.NoError(t, err)
	assert.True(t, got.Equal(k))

	_, err = ParsePrivateKey([]byte("lixo"))
	assert.Error(t, err)
}
