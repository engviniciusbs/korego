package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func reqWith(remote, xff string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remote
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func TestClientIP_IgnoresXFFFromRemote(t *testing.T) {
	require.Equal(t, "203.0.113.10", ClientIP(reqWith("203.0.113.10:5000", "1.2.3.4")))
}

func TestClientIP_TrustsXFFFromLoopback(t *testing.T) {
	require.Equal(t, "10.0.0.1", ClientIP(reqWith("127.0.0.1:5000", "198.51.100.7, 10.0.0.1")))
	require.Equal(t, "198.51.100.7", ClientIP(reqWith("[::1]:5000", " 198.51.100.7 ")))
}

func TestClientIP_Fallbacks(t *testing.T) {
	require.Equal(t, "127.0.0.1", ClientIP(reqWith("127.0.0.1:5000", "not-an-ip")))
	require.Equal(t, "127.0.0.1", ClientIP(reqWith("127.0.0.1:5000", "")))
	require.Equal(t, "203.0.113.10", ClientIP(reqWith("203.0.113.10", "")))
}

func TestClientIP_IgnoresSpoofedLeftmost(t *testing.T) {
	require.Equal(t, "198.51.100.7", ClientIP(reqWith("127.0.0.1:5000", "6.6.6.6, 198.51.100.7")))
}

// Domínio só Cloudflare: o Caddy (par loopback) troca o X-Forwarded-For por
// {http.vars.client_ip}, o visitante tirado de CF-Connecting-IP. O painel vê o
// visitante, não o IP da Cloudflare; de um par remoto o cabeçalho segue ignorado.
func TestClientIP_CloudflareOnlyClientIPFromLoopback(t *testing.T) {
	require.Equal(t, "203.0.113.10", ClientIP(reqWith("127.0.0.1:41234", "203.0.113.10")))
	require.Equal(t, "2001:db8::7", ClientIP(reqWith("127.0.0.1:41234", "2001:db8::7")))
	require.Equal(t, "198.51.100.20", ClientIP(reqWith("198.51.100.20:443", "203.0.113.10")))
}
