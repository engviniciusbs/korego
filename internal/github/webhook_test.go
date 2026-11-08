package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "segredo-de-teste-do-webhook"

func sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func pushPayload(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "api", "testdata", "push.json"))
	require.NoError(t, err)
	return b
}

func TestVerifySignature(t *testing.T) {
	body := pushPayload(t)
	sig := sign(testSecret, body)
	assert.True(t, VerifySignature([]byte(testSecret), body, sig))

	altered := append([]byte(nil), body...)
	altered[10] ^= 0x01
	assert.False(t, VerifySignature([]byte(testSecret), altered, sig))
	assert.False(t, VerifySignature([]byte("outro-segredo"), body, sig))
	assert.False(t, VerifySignature([]byte(testSecret), body, strings.TrimPrefix(sig, "sha256=")))
	assert.False(t, VerifySignature([]byte(testSecret), body, "sha1="+strings.TrimPrefix(sig, "sha256=")))
	assert.False(t, VerifySignature([]byte(testSecret), body, "sha256=zz"+sig[9:]))
	assert.False(t, VerifySignature([]byte(testSecret), body, ""))
	// Segredo vazio nunca valida, nem com a assinatura "certa".
	assert.False(t, VerifySignature(nil, body, sign("", body)))
}

func TestParsePush(t *testing.T) {
	p, err := ParsePush(pushPayload(t))
	require.NoError(t, err)
	assert.Equal(t, Push{Repo: "exemplo/site", Branch: "main", After: "0d1a26e67d8f5eaf1f6ba5c57fc3c7d91ac0fd1c",
		Message: "Atualiza a página inicial"}, p)

	// Tag: ignorada.
	_, err = ParsePush([]byte(`{"ref":"refs/tags/v1.0.0","after":"0d1a26e67d8f5eaf1f6ba5c57fc3c7d91ac0fd1c","repository":{"full_name":"exemplo/site"}}`))
	assert.ErrorIs(t, err, ErrNotBranch)

	// Branch apagada: after só com zeros e head_commit nulo.
	p, err = ParsePush([]byte(`{"ref":"refs/heads/feature/x","after":"0000000000000000000000000000000000000000","deleted":true,"head_commit":null,"repository":{"full_name":"exemplo/site"}}`))
	require.NoError(t, err)
	assert.True(t, p.Deleted)
	assert.Equal(t, "feature/x", p.Branch)
	assert.Equal(t, "", p.Message)

	// Mensagem longa: primeira linha, cortada em 200 caracteres.
	long := strings.Repeat("ç", 250)
	p, err = ParsePush([]byte(`{"ref":"refs/heads/main","after":"abc123","head_commit":{"message":"` + long + `\nresto"},"repository":{"full_name":"exemplo/site"}}`))
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("ç", 200), p.Message)
	assert.False(t, p.Deleted)

	for _, bad := range []string{
		`não é json`,
		`{"ref":"refs/heads/","after":"abc","repository":{"full_name":"exemplo/site"}}`,
		`{"ref":"refs/heads/main","after":"abc","repository":{"full_name":""}}`,
		`{"ref":"refs/heads/main","after":"","repository":{"full_name":"exemplo/site"}}`,
	} {
		_, err := ParsePush([]byte(bad))
		assert.Error(t, err, bad)
		assert.NotErrorIs(t, err, ErrNotBranch, bad)
	}
}
