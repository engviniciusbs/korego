package migrate

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/proxy"
)

func TestCheck_ReportsStatusAndLeaf(t *testing.T) {
	var (
		mu    sync.Mutex
		hosts []string
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	}))
	defer srv.Close()
	addr := srv.Listener.Addr().String()

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
	assert.Contains(t, a.Leaf.Names, "example.com") // certificado do httptest
	assert.False(t, a.Leaf.NotAfter.IsZero())
	assert.True(t, a.TLSOK)

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
		{Host: "exemplo.com", TLS: proxy.SiteTLS{Mode: proxy.TLSCustom}, Routes: []proxy.SiteRoute{{Path: "/", Upstream: "127.0.0.1:1"}}},
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
