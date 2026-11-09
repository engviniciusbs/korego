package migrate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/proxy"
)

// tlsServer sobe um servidor TLS com um certificado autoassinado para names,
// válido até notAfter.
func tlsServer(t *testing.T, h http.Handler, notAfter time.Time, names ...string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: names[0]},
		Issuer:       pkix.Name{CommonName: "teste"},
		DNSNames:     names,
		NotBefore:    notAfter.Add(-90 * 24 * time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	require.NoError(t, err)
	srv := httptest.NewUnstartedServer(h)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

func okHandler(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func TestCheck_ReportsStatusAndLeaf(t *testing.T) {
	var (
		mu    sync.Mutex
		hosts []string
	)
	addr := tlsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hosts = append(hosts, r.Host+" "+r.TLS.ServerName)
		mu.Unlock()
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write([]byte("<html></html>"))
		case "/api/":
			w.WriteHeader(http.StatusBadGateway)
		case "/old/":
			http.Redirect(w, r, "/novo/", http.StatusMovedPermanently)
		default:
			http.NotFound(w, r)
		}
	}), time.Now().Add(30*24*time.Hour), "exemplo.com", "loja.exemplo.net")

	sites := []proxy.Site{
		{Host: "exemplo.com", TLS: proxy.SiteTLS{Mode: proxy.TLSCustom}, Routes: []proxy.SiteRoute{
			{Path: "/", Static: &proxy.StaticTarget{Root: "/var/www/exemplo"}},
			{Path: "/old/", Upstream: "127.0.0.1:20002"},
		}},
		{Host: "loja.exemplo.net", TLS: proxy.SiteTLS{Mode: proxy.TLSCustom}, Routes: []proxy.SiteRoute{
			{Path: "/", Upstream: "127.0.0.1:20001"},
			{Path: "/api/", Upstream: "127.0.0.1:20003"},
		}},
	}
	got := Check(context.Background(), sites, addr, true)
	require.Len(t, got, 2)

	a := got[0]
	assert.Equal(t, "exemplo.com", a.Host)
	assert.True(t, a.OK, a.Message)
	assert.False(t, a.Pending)
	require.Len(t, a.Paths, 2)
	assert.Equal(t, "/", a.Paths[0].Path)
	assert.Equal(t, http.StatusOK, a.Paths[0].Status)
	assert.True(t, a.Paths[0].Static)
	assert.True(t, a.Paths[0].Index)
	assert.Equal(t, "no-cache", a.Paths[0].Headers["Cache-Control"])
	assert.Equal(t, http.StatusMovedPermanently, a.Paths[1].Status, "redirecionamento não é seguido")
	assert.Equal(t, "/novo/", a.Paths[1].Headers["Location"])
	assert.Contains(t, a.Leaf.Names, "exemplo.com")
	assert.Equal(t, "exemplo.com", a.Leaf.Issuer) // autoassinado
	assert.False(t, a.Leaf.NotAfter.IsZero())
	assert.True(t, a.TLSOK)
	assert.True(t, a.LeafOK)

	b := got[1]
	assert.Equal(t, "loja.exemplo.net", b.Host)
	assert.False(t, b.OK)
	assert.Equal(t, http.StatusBadGateway, b.Paths[1].Status)
	assert.False(t, b.Paths[1].OK)
	assert.Contains(t, b.Message, "/api/")
	assert.Contains(t, b.Message, "502")

	// SNI e Host são o host do site.
	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, hosts, "exemplo.com exemplo.com")
	assert.Contains(t, hosts, "loja.exemplo.net loja.exemplo.net")
}

func custom(host string) proxy.Site {
	return proxy.Site{Host: host, TLS: proxy.SiteTLS{Mode: proxy.TLSCustom},
		Routes: []proxy.SiteRoute{{Path: "/", Upstream: "127.0.0.1:20001"}}}
}

func TestCheck_WrongLeafFails(t *testing.T) {
	addr := tlsServer(t, http.HandlerFunc(okHandler), time.Now().Add(30*24*time.Hour), "outro.exemplo.org")
	for _, trial := range []bool{true, false} {
		got := Check(context.Background(), []proxy.Site{custom("exemplo.com")}, addr, trial)
		require.Len(t, got, 1)
		assert.False(t, got[0].OK, "trial=%v", trial)
		assert.True(t, got[0].TLSOK)
		assert.False(t, got[0].LeafOK)
		assert.Contains(t, got[0].Message, "não cobre exemplo.com")
	}
}

func TestCheck_ExpiredLeafFails(t *testing.T) {
	addr := tlsServer(t, http.HandlerFunc(okHandler), time.Now().Add(-time.Hour), "exemplo.com")
	got := Check(context.Background(), []proxy.Site{custom("exemplo.com")}, addr, false)
	require.Len(t, got, 1)
	assert.False(t, got[0].OK)
	assert.False(t, got[0].LeafOK)
	assert.Contains(t, got[0].Message, "vencido")
}

func TestCheck_403OnlyPassesForCloudflareOnly(t *testing.T) {
	forbidden := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	addr := tlsServer(t, forbidden, time.Now().Add(30*24*time.Hour), "exemplo.com", "loja.exemplo.net")
	cf := custom("loja.exemplo.net")
	cf.CloudflareOnly = true
	got := Check(context.Background(), []proxy.Site{custom("exemplo.com"), cf}, addr, false)
	require.Len(t, got, 2)
	assert.False(t, got[0].OK, "403 reprova um host comum")
	assert.Contains(t, got[0].Message, "403")
	assert.True(t, got[1].OK, "403 passa num host só Cloudflare com a folha certa: %s", got[1].Message)

	// Só Cloudflare com a folha errada continua reprovado.
	addr = tlsServer(t, forbidden, time.Now().Add(30*24*time.Hour), "outro.exemplo.org")
	got = Check(context.Background(), []proxy.Site{cf}, addr, false)
	assert.False(t, got[0].OK)
}

func TestCheck_HandshakeFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close() // derruba o handshake
		}
	}()
	defer ln.Close()
	addr := ln.Addr().String()
	sites := []proxy.Site{
		custom("exemplo.com"),
		{Host: "loja.exemplo.net", Routes: []proxy.SiteRoute{{Path: "/", Upstream: "127.0.0.1:1"}}}, // auto
	}

	got := Check(context.Background(), sites, addr, true)
	require.Len(t, got, 2)
	assert.False(t, got[0].OK)
	assert.False(t, got[0].TLSOK)
	assert.Contains(t, got[0].Message, "TLS")
	// auto em ensaio: certificado pendente, não falha.
	assert.True(t, got[1].OK, got[1].Message)
	assert.True(t, got[1].Pending)

	// Fora do ensaio, o auto falha (a espera fica com o motor).
	got = Check(context.Background(), sites, addr, false)
	assert.False(t, got[1].OK)
	assert.False(t, got[1].Pending)
}
