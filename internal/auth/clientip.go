package auth

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP returns the caller's IP. X-Forwarded-For is honored only when the
// request comes from a loopback address (a local reverse proxy).
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	host = strings.TrimSpace(host)
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		first, _, _ := strings.Cut(r.Header.Get("X-Forwarded-For"), ",")
		if x := net.ParseIP(strings.TrimSpace(first)); x != nil {
			return x.String()
		}
	}
	return host
}
