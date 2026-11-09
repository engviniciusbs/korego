package api_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/certs"
	"github.com/engviniciusbs/korego/internal/secrets"
	"github.com/engviniciusbs/korego/internal/store"
)

// genCertPEM gera um certificado autoassinado + chave EC num único texto PEM.
func genCertPEM(t *testing.T, name string, notAfter time.Time) string {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<60))
	require.NoError(t, err)
	tpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter, DNSNames: []string{name},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	require.NoError(t, err)
	kd, err := x509.MarshalECPrivateKey(k)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) +
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}))
}

func certsEnv(t *testing.T) (*env, *certs.Service, *http.Cookie, string) {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	box, err := secrets.NewBox(key)
	require.NoError(t, err)
	svc := &certs.Service{DB: store.OpenMemory(t), Box: box}
	e := newEnvDeps(t, api.Deps{Certs: svc})
	_, c, csrf := e.setup(t)
	return e, svc, c, csrf
}

func TestAPI_CertificateNeverReturnsKey(t *testing.T) {
	e, _, c, csrf := certsEnv(t)
	good := genCertPEM(t, "exemplo.com", time.Now().Add(90*24*time.Hour))

	w := e.do(t, http.MethodPost, "/api/certificates", map[string]any{"name": "meu", "pem": good}, mut(c, csrf)...)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), "PRIVATE KEY")
	b := decode(t, w)["certificate"].(map[string]any)
	assert.Equal(t, "meu", b["name"])
	assert.Equal(t, []any{"exemplo.com"}, b["names"])
	assert.Regexp(t, `^([0-9A-F]{2}:){31}[0-9A-F]{2}$`, b["fingerprint"])
	assert.Equal(t, false, b["expiresSoon"])
	assert.Equal(t, []any{}, b["usedBy"])
	for _, k := range []string{"id", "issuer", "notAfter"} {
		assert.Contains(t, b, k)
	}
	for k := range b {
		assert.NotContains(t, strings.ToLower(k), "pem")
	}

	w = e.do(t, http.MethodGet, "/api/certificates", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "PRIVATE KEY")
	assert.NotContains(t, w.Body.String(), "BEGIN")
	require.Len(t, decode(t, w)["certificates"].([]any), 1)

	// Erros também nunca devolvem a chave.
	w = e.do(t, http.MethodPost, "/api/certificates", map[string]any{"name": "dup", "pem": good}, mut(c, csrf)...)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "INVALID_CERT", errCode(decode(t, w)))
	assert.Equal(t, "Este certificado já está cadastrado.",
		decode(t, w)["error"].(map[string]any)["message"])
	assert.NotContains(t, w.Body.String(), "PRIVATE KEY")

	w = e.do(t, http.MethodDelete, "/api/certificates/1", nil, mut(c, csrf)...)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.NotContains(t, w.Body.String(), "PRIVATE KEY")
}

func TestAPI_CertificateErrors(t *testing.T) {
	e, _, c, csrf := certsEnv(t)
	post := func(body map[string]any) map[string]any {
		w := e.do(t, http.MethodPost, "/api/certificates", body, mut(c, csrf)...)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		return decode(t, w)
	}
	assert.Equal(t, "INVALID_CERT", errCode(post(map[string]any{"name": "x", "pem": "lixo"})))
	assert.Equal(t, "INVALID_CERT", errCode(post(map[string]any{"name": "", "pem": "lixo"})))
	assert.Equal(t, "INVALID_CERT", errCode(post(map[string]any{"name": "x"})))
	assert.Equal(t, "INVALID_CERT", errCode(post(map[string]any{"name": "x", "pem": "a", "path": "/b"})))
	assert.Equal(t, "CERT_EXPIRED", errCode(post(map[string]any{"name": "x",
		"pem": genCertPEM(t, "exemplo.com", time.Now().Add(-time.Hour))})))

	a, b := genCertPEM(t, "exemplo.com", time.Now().Add(time.Hour*24)), genCertPEM(t, "exemplo.com", time.Now().Add(time.Hour*24))
	ai, bi := strings.Index(a, "-----BEGIN EC"), strings.Index(b, "-----BEGIN EC")
	assert.Equal(t, "CERT_MISMATCH", errCode(post(map[string]any{"name": "x", "pem": a[:ai] + b[bi:]})))

	w := e.do(t, http.MethodDelete, "/api/certificates/abc", nil, mut(c, csrf)...)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = e.do(t, http.MethodDelete, "/api/certificates/99", nil, mut(c, csrf)...)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "CERT_NOT_FOUND", errCode(decode(t, w)))

	// Sem sessão e sem CSRF.
	assert.Equal(t, http.StatusUnauthorized, e.do(t, http.MethodGet, "/api/certificates", nil).Code)
	assert.Equal(t, http.StatusForbidden,
		e.do(t, http.MethodPost, "/api/certificates", map[string]any{"name": "x"}, withCookie(c)).Code)
}

func TestAPI_DeleteInUse409(t *testing.T) {
	e, svc, c, csrf := certsEnv(t)
	w := e.do(t, http.MethodPost, "/api/certificates",
		map[string]any{"name": "meu", "pem": genCertPEM(t, "exemplo.com", time.Now().Add(90*24*time.Hour))},
		mut(c, csrf)...)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	id := int64(decode(t, w)["certificate"].(map[string]any)["id"].(float64))
	d := &store.Domain{Hostname: "exemplo.com", TargetType: "address", Address: "127.0.0.1:5000",
		WWWRedirect: "none", TLSMode: "custom", CertID: &id}
	require.NoError(t, svc.DB.InsertDomain(t.Context(), d))

	w = e.do(t, http.MethodGet, "/api/certificates", nil, withCookie(c))
	cert := decode(t, w)["certificates"].([]any)[0].(map[string]any)
	assert.Equal(t, []any{"exemplo.com"}, cert["usedBy"])

	// O brief fixa o código CERT_IN_USE (400, na tabela de erros do plano).
	w = e.do(t, http.MethodDelete, "/api/certificates/1", nil, mut(c, csrf)...)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Equal(t, "CERT_IN_USE", errCode(decode(t, w)))
}

func TestAPI_AddFromPath(t *testing.T) {
	e, _, c, csrf := certsEnv(t)
	file := filepath.Join(t.TempDir(), "bundle.pem")
	require.NoError(t, os.WriteFile(file, []byte(genCertPEM(t, "loja.exemplo.net", time.Now().Add(90*24*time.Hour))), 0o600))

	w := e.do(t, http.MethodPost, "/api/certificates", map[string]any{"name": "disco", "path": file}, mut(c, csrf)...)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), "PRIVATE KEY")
	assert.Equal(t, []any{"loja.exemplo.net"}, decode(t, w)["certificate"].(map[string]any)["names"])

	w = e.do(t, http.MethodPost, "/api/certificates",
		map[string]any{"name": "x", "path": filepath.Join(filepath.Dir(file), "nao-existe.pem")}, mut(c, csrf)...)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "INVALID_CERT", errCode(decode(t, w)))
	assert.Contains(t, decode(t, w)["error"].(map[string]any)["message"], "Não foi possível ler o arquivo")

	w = e.do(t, http.MethodPost, "/api/certificates", map[string]any{"name": "x", "path": "relativo.pem"}, mut(c, csrf)...)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "INVALID_CERT", errCode(decode(t, w)))
}

func TestAPI_CertificateExpiresSoon(t *testing.T) {
	e, _, c, csrf := certsEnv(t)
	w := e.do(t, http.MethodPost, "/api/certificates",
		map[string]any{"name": "perto", "pem": genCertPEM(t, "exemplo.com", time.Now().Add(5*24*time.Hour))},
		mut(c, csrf)...)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Equal(t, true, decode(t, w)["certificate"].(map[string]any)["expiresSoon"])
}

func TestAPI_CertificatesAudited(t *testing.T) {
	e, _, c, csrf := certsEnv(t)
	good := genCertPEM(t, "exemplo.com", time.Now().Add(90*24*time.Hour))
	e.do(t, http.MethodPost, "/api/certificates", map[string]any{"name": "meu", "pem": good}, mut(c, csrf)...)
	e.do(t, http.MethodPost, "/api/certificates", map[string]any{"name": "meu", "pem": good}, mut(c, csrf)...) // duplicado
	e.do(t, http.MethodDelete, "/api/certificates/1", nil, mut(c, csrf)...)

	w := e.do(t, http.MethodGet, "/api/audit", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "PRIVATE KEY")
	assert.NotContains(t, w.Body.String(), "BEGIN CERTIFICATE")
	type key struct {
		action string
		ok     bool
	}
	got := map[key]map[string]any{}
	for _, it := range decode(t, w)["items"].([]any) {
		m := it.(map[string]any)
		md, _ := m["metadata"].(map[string]any)
		ok, _ := md["ok"].(bool)
		got[key{m["action"].(string), ok}] = md
	}
	create := got[key{"certificate.create", true}]
	require.NotNil(t, create, got)
	assert.Equal(t, "meu", create["name"])
	assert.Regexp(t, `^([0-9A-F]{2}:){31}[0-9A-F]{2}$`, create["fingerprint"])
	assert.NotNil(t, got[key{"certificate.create", false}], got)
	del := got[key{"certificate.delete", true}]
	require.NotNil(t, del, got)
	assert.Equal(t, "meu", del["name"])
	assert.NotEmpty(t, del["fingerprint"])
}
