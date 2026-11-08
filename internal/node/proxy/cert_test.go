package proxy

import (
	"context"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/docker/dockertest"
)

func TestCert_StatesWithTestServer(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	addr := srv.Listener.Addr().String()
	ctx := context.Background()

	// Certificado assinado por uma raiz confiável → active.
	trusted := x509.NewCertPool()
	trusted.AddCert(srv.Certificate())
	c := NewCaddy(&dockertest.Fake{}, downAdmin(t))
	c.CertAddr, c.CertRoots, c.CertTimeout = addr, trusted, 2*time.Second
	info := c.Cert(ctx, "example.com")
	assert.Equal(t, CertActive, info.State, info.Message)
	assert.True(t, srv.Certificate().NotAfter.Equal(info.NotAfter))
	assert.NotEmpty(t, info.Issuer)

	// Raiz desconhecida (como o certificado interno do Caddy) → pending.
	c.CertRoots = x509.NewCertPool()
	info = c.Cert(ctx, "example.com")
	assert.Equal(t, CertPending, info.State)
	assert.True(t, info.NotAfter.IsZero())

	// Nada escutando → error.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closed := l.Addr().String()
	require.NoError(t, l.Close())
	c.CertAddr = closed
	info = c.Cert(ctx, "example.com")
	assert.Equal(t, CertError, info.State)
	assert.NotEmpty(t, info.Message)
}
