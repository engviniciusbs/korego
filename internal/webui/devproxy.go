package webui

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// NewDevProxy devolve um proxy reverso para o servidor `next dev`.
// O ReverseProxy repassa upgrades de WebSocket (HMR) nativamente.
func NewDevProxy(target string) (http.Handler, error) {
	u, err := url.Parse(target)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("URL do proxy de desenvolvimento inválida: %q", target)
	}
	p := httputil.NewSingleHostReverseProxy(u)
	director := p.Director
	p.Director = func(r *http.Request) {
		director(r)
		r.Host = u.Host
	}
	return p, nil
}
