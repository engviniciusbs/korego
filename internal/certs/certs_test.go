package certs_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/certs"
	"github.com/engviniciusbs/korego/internal/secrets"
	"github.com/engviniciusbs/korego/internal/store"
)

var testNow = time.Unix(1_800_000_000, 0)

type genOpts struct {
	names    []string
	notAfter time.Time
	keyKind  string // ec (padrão) | rsa-pkcs1 | pkcs8
}

// gen cria um certificado autoassinado e devolve (certPEM, keyPEM).
func gen(t *testing.T, o genOpts) ([]byte, []byte) {
	t.Helper()
	if o.notAfter.IsZero() {
		o.notAfter = testNow.Add(90 * 24 * time.Hour)
	}
	if len(o.names) == 0 {
		o.names = []string{"exemplo.com"}
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<60))
	require.NoError(t, err)
	tpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: o.names[0], Organization: []string{"Teste"}},
		NotBefore: testNow.Add(-24 * time.Hour), NotAfter: o.notAfter, DNSNames: o.names,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	var priv, pub any
	var keyPEM []byte
	switch o.keyKind {
	case "rsa-pkcs1":
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		priv, pub = k, &k.PublicKey
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
	case "pkcs8":
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		priv, pub = k, &k.PublicKey
		der, err := x509.MarshalPKCS8PrivateKey(k)
		require.NoError(t, err)
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	default:
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		priv, pub = k, &k.PublicKey
		der, err := x509.MarshalECPrivateKey(k)
		require.NoError(t, err)
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, pub, priv)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), keyPEM
}

func join(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
		out = append(out, '\n')
	}
	return out
}

func TestParse(t *testing.T) {
	cert, key := gen(t, genOpts{names: []string{"exemplo.com", "*.exemplo.com"}})
	_, otherKey := gen(t, genOpts{})
	expCert, expKey := gen(t, genOpts{notAfter: testNow.Add(-time.Hour)})
	rsaCert, rsaKey := gen(t, genOpts{keyKind: "rsa-pkcs1"})
	p8Cert, p8Key := gen(t, genOpts{keyKind: "pkcs8"})

	encKey := pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte("x")})
	legacyEnc := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY",
		Headers: map[string]string{"Proc-Type": "4,ENCRYPTED", "DEK-Info": "AES-128-CBC,00"}, Bytes: []byte("x")})

	tests := []struct {
		name string
		in   []byte
		want error
	}{
		{"cadeia e chave", join(cert, key), nil},
		{"chave antes da cadeia", join(key, cert), nil},
		{"RSA PKCS1", join(rsaCert, rsaKey), nil},
		{"PKCS8", join(p8Cert, p8Key), nil},
		{"chave de outro certificado", join(cert, otherKey), certs.ErrKeyMismatch},
		{"só o certificado", cert, certs.ErrInvalidCert},
		{"só a chave", key, certs.ErrInvalidCert},
		{"lixo", []byte("isto não é PEM"), certs.ErrInvalidCert},
		{"vazio", nil, certs.ErrInvalidCert},
		{"chave cifrada", join(cert, encKey), certs.ErrInvalidCert},
		{"chave cifrada legada", join(cert, legacyEnc), certs.ErrInvalidCert},
		{"expirado", join(expCert, expKey), certs.ErrExpired},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info, err := certs.Parse(tc.in, testNow)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, info.Fingerprint)
			assert.NotEmpty(t, info.Names)
			assert.True(t, info.NotAfter.After(testNow))
		})
	}

	t.Run("campos", func(t *testing.T) {
		info, err := certs.Parse(join(key, cert), testNow)
		require.NoError(t, err)
		assert.Equal(t, []string{"exemplo.com", "*.exemplo.com"}, info.Names)
		assert.Regexp(t, `^([0-9A-F]{2}:){31}[0-9A-F]{2}$`, info.Fingerprint)
		assert.Contains(t, info.Issuer, "exemplo.com")
		// O PEM normalizado tem o certificado primeiro e a chave por último.
		assert.True(t, strings.HasPrefix(string(info.PEM), "-----BEGIN CERTIFICATE-----"))
		assert.Contains(t, string(info.PEM), "PRIVATE KEY")
		_, err = certs.Parse(info.PEM, testNow)
		require.NoError(t, err)
	})
	t.Run("mensagem de chave cifrada", func(t *testing.T) {
		_, err := certs.Parse(join(cert, encKey), testNow)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cifrada")
	})
}

func TestCovers(t *testing.T) {
	tests := []struct {
		names []string
		host  string
		want  bool
	}{
		{[]string{"*.exemplo.com"}, "a.exemplo.com", true},
		{[]string{"*.exemplo.com"}, "A.Exemplo.COM", true},
		{[]string{"*.exemplo.com"}, "a.b.exemplo.com", false},
		{[]string{"*.exemplo.com"}, "exemplo.com", false},
		{[]string{"*.exemplo.com", "exemplo.com"}, "exemplo.com", true},
		{[]string{"Exemplo.com"}, "exemplo.com", true},
		{[]string{"exemplo.com"}, "www.exemplo.com", false},
		{[]string{"*.exemplo.com"}, ".exemplo.com", false},
		{[]string{"exemplo.com"}, "", false},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, certs.Covers(tc.names, tc.host), "%v %q", tc.names, tc.host)
	}
}

func newSvc(t *testing.T) *certs.Service {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	box, err := secrets.NewBox(key)
	require.NoError(t, err)
	return &certs.Service{DB: store.OpenMemory(t), Box: box, Now: func() time.Time { return testNow }}
}

func TestService_AddListPEMDelete(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	cert, key := gen(t, genOpts{names: []string{"exemplo.com"}})
	in := join(cert, key)

	c, err := s.Add(ctx, "meu", in)
	require.NoError(t, err)
	assert.NotZero(t, c.ID)
	assert.NotContains(t, c.PEMEnc, "PRIVATE KEY", "gravado cifrado")

	_, err = s.Add(ctx, "outra", in)
	require.ErrorIs(t, err, certs.ErrDuplicate)
	assert.Equal(t, "Este certificado já está cadastrado.", err.Error())

	list, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Empty(t, list[0].UsedBy)
	assert.False(t, list[0].ExpiresSoon)

	pemText, err := s.PEM(ctx, c.ID)
	require.NoError(t, err)
	_, err = certs.Parse([]byte(pemText), testNow)
	require.NoError(t, err)

	require.NoError(t, s.Delete(ctx, c.ID))
	require.ErrorIs(t, s.Delete(ctx, c.ID), store.ErrNotFound)
}

func TestService_ExpiresSoon(t *testing.T) {
	s := newSvc(t)
	cert, key := gen(t, genOpts{notAfter: testNow.Add(10 * 24 * time.Hour)})
	_, err := s.Add(context.Background(), "perto", join(cert, key))
	require.NoError(t, err)
	list, err := s.List(context.Background())
	require.NoError(t, err)
	assert.True(t, list[0].ExpiresSoon)
}

func TestService_InUse(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	cert, key := gen(t, genOpts{})
	c, err := s.Add(ctx, "meu", join(cert, key))
	require.NoError(t, err)
	d := &store.Domain{Hostname: "exemplo.com", TargetType: "address", Address: "127.0.0.1:5000",
		WWWRedirect: "none", TLSMode: "custom", CertID: &c.ID}
	require.NoError(t, s.DB.InsertDomain(ctx, d))
	list, err := s.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"exemplo.com"}, list[0].UsedBy)
	require.ErrorIs(t, s.Delete(ctx, c.ID), store.ErrCertInUse)
}

func TestService_AddFromPath(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	cert, key := gen(t, genOpts{})
	dir := t.TempDir()
	good := filepath.Join(dir, "bundle.pem")
	require.NoError(t, os.WriteFile(good, join(cert, key), 0o600))
	c, err := s.AddFromPath(ctx, "do-disco", good)
	require.NoError(t, err)
	assert.Equal(t, "do-disco", c.Name)

	_, err = s.AddFromPath(ctx, "x", "relativo/arquivo.pem")
	require.ErrorIs(t, err, certs.ErrInvalidCert)
	assert.Equal(t, readFail, err.Error())

	_, err = s.AddFromPath(ctx, "x", filepath.Join(dir, "nao-existe.pem"))
	require.ErrorIs(t, err, certs.ErrInvalidCert)
	assert.Equal(t, readFail, err.Error())

	bigFile := filepath.Join(dir, "grande.pem")
	require.NoError(t, os.WriteFile(bigFile, []byte(strings.Repeat("A", 1<<20+10)), 0o600))
	_, err = s.AddFromPath(ctx, "x", bigFile)
	require.ErrorIs(t, err, certs.ErrInvalidCert)
	assert.Equal(t, readFail, err.Error())

	secret := filepath.Join(dir, "lixo.pem")
	require.NoError(t, os.WriteFile(secret, []byte("SEGREDO-NO-ARQUIVO"), 0o600))
	_, err = s.AddFromPath(ctx, "x", secret)
	require.ErrorIs(t, err, certs.ErrInvalidCert)
	assert.NotContains(t, err.Error(), "SEGREDO-NO-ARQUIVO")
}

const readFail = "Não foi possível ler o arquivo informado."

func TestService_AddFromPath_Directory(t *testing.T) {
	s := newSvc(t)
	_, err := s.AddFromPath(context.Background(), "x", t.TempDir())
	require.ErrorIs(t, err, certs.ErrInvalidCert)
	assert.Equal(t, readFail, err.Error())
}

func TestService_Get(t *testing.T) {
	s := newSvc(t)
	cert, key := gen(t, genOpts{})
	c, err := s.Add(context.Background(), "meu", join(cert, key))
	require.NoError(t, err)
	got, err := s.Get(context.Background(), c.ID)
	require.NoError(t, err)
	assert.Equal(t, "meu", got.Name)
	_, err = s.Get(context.Background(), 999)
	require.ErrorIs(t, err, store.ErrNotFound)
}
