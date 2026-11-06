package auth

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP returns the caller's IP. X-Forwarded-For is honored only when the
// request comes from a loopback address (a local reverse proxy). The rightmost
// entry is used because it is the one appended by that trusted proxy; the
// leftmost is client-supplied and can be spoofed to evade the rate limit.
func ClientIP(r *http.Request) string {
	host := remoteHost(r)
	if FromLoopback(r) {
		xff := r.Header.Get("X-Forwarded-For")
		last := xff[strings.LastIndex(xff, ",")+1:]
		if x := net.ParseIP(strings.TrimSpace(last)); x != nil {
			return x.String()
		}
	}
	return host
}

// FromLoopback reports whether the request's direct peer is a loopback address,
// i.e. a local reverse proxy whose forwarding headers can be trusted.
func FromLoopback(r *http.Request) bool {
	ip := net.ParseIP(remoteHost(r))
	return ip != nil && ip.IsLoopback()
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return strings.TrimSpace(host)
}
