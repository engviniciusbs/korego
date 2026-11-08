package api

import (
	"net/http"
	"net/url"
)

const cspBase = "default-src 'self'; script-src 'self' 'unsafe-inline'; " +
	"style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; " +
	"frame-ancestors 'none'; base-uri 'self'; form-action 'self' "

const defaultGitHubOrigin = "https://github.com"

// githubOrigin reduz a URL web do GitHub a esquema e host. Uma URL vazia ou
// inválida cai na origem padrão.
func githubOrigin(webURL string) string {
	u, err := url.Parse(webURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return defaultGitHubOrigin
	}
	return u.Scheme + "://" + u.Host
}

// SecurityHeaders adiciona os cabeçalhos de segurança a todas as respostas.
// githubWebURL é a URL web do GitHub configurada: o form-action libera a sua
// origem, porque o fluxo do manifesto do GitHub App envia um formulário para lá.
func SecurityHeaders(next http.Handler, githubWebURL string) http.Handler {
	csp := cspBase + githubOrigin(githubWebURL)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
