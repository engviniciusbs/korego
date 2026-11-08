package proxy

// Configuração JSON nativa do Caddy (não Caddyfile).
//
// Formato escolhido e por que o HTTPS automático continua funcionando
// (verificado em 2026-11-08):
//
//   - Servidor "korego_https" em :443 com automatic_https.disable_redirects.
//     O Caddy coleta os hostnames dos matchers "host" das rotas e gerencia
//     os certificados deles; disable_redirects desliga só os redirecionamentos
//     automáticos HTTP→HTTPS: "If true, only automatic HTTP->HTTPS redirects
//     will be disabled, but other auto-HTTPS features will remain enabled."
//     https://caddyserver.com/docs/json/apps/http/servers/automatic_https/disable_redirects/
//     https://caddyserver.com/docs/automatic-https ("A host matcher at the
//     top-level in the JSON routes")
//
//   - Servidor "korego_http" em :80 definido por nós. Um servidor que só escuta
//     na porta HTTP fica, por conta do próprio Caddy, sem HTTPS automático
//     (autohttps.go: "server is listening only on the HTTP port, so no
//     automatic HTTPS will be applied to this server"). Isso não afeta os
//     desafios HTTP-01: Server.ServeHTTP responde os desafios ACME antes de
//     qualquer rota do usuário, em todo servidor ("guarantee ACME HTTP
//     challenges; handle them separately from any user-defined handlers",
//     modules/caddyhttp/server.go, chamada tlsApp.HandleHTTPChallenge).
//     https://github.com/caddyserver/caddy/blob/master/modules/caddyhttp/server.go
//     https://github.com/caddyserver/caddy/blob/master/modules/caddyhttp/autohttps.go
//     Por isso NÃO usamos automatic_https.disable no :443 (desligaria também
//     os certificados) nem precisamos de nada no :80.
//
//   - Como disable_redirects está ligado, o Caddy não injeta rotas de
//     redirecionamento no :80; quem decide é a rota de cada host: 308 para
//     https quando ForceHTTPS, senão o mesmo reverse_proxy (HTTP puro).
//
//   - apps.tls.automation.policies com "subjects" explícitos cobre todos os
//     hosts (inclusive o secundário do www). O Caddy respeita políticas
//     explícitas para esses nomes ("some names we've found might already have
//     automation policies explicitly specified for them", autohttps.go); sem
//     "issuers", valem os emissores ACME padrão.
//
//   - Os dois servidores terminam com uma rota sem matcher (static_response
//     404 "Domínio não configurado no KoreGO."): sem ela o Caddy responde um
//     200 vazio a hosts não cadastrados. Os desafios HTTP-01 não passam por
//     ela, porque o Caddy os atende antes das rotas (ver acima).
//
//   - reverse_proxy mantém o Host original e define X-Forwarded-For/-Proto/
//     -Host por padrão, então não acrescentamos cabeçalhos.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// AdminListen é o endereço da Admin API dentro da config gerada.
const AdminListen = "127.0.0.1:2019"

const (
	httpsServerName = "korego_https"
	httpServerName  = "korego_http"
	forceHTTPSURL   = "https://{http.request.host}{http.request.uri}"
	// notConfiguredBody é a resposta da rota final para hosts não cadastrados.
	notConfiguredBody = "Domínio não configurado no KoreGO."
)

type caddyConfig struct {
	Admin caddyAdmin `json:"admin"`
	Apps  caddyApps  `json:"apps"`
}

type caddyAdmin struct {
	Listen string `json:"listen"`
}

type caddyApps struct {
	HTTP caddyHTTPApp `json:"http"`
	TLS  *caddyTLSApp `json:"tls,omitempty"`
}

type caddyHTTPApp struct {
	Servers map[string]caddyServer `json:"servers"`
}

type caddyServer struct {
	Listen         []string        `json:"listen"`
	AutomaticHTTPS *caddyAutoHTTPS `json:"automatic_https,omitempty"`
	Routes         []caddyRoute    `json:"routes"`
}

type caddyAutoHTTPS struct {
	DisableRedirects bool `json:"disable_redirects,omitempty"`
}

type caddyRoute struct {
	Match    []caddyMatch   `json:"match,omitempty"`
	Handle   []caddyHandler `json:"handle"`
	Terminal bool           `json:"terminal"`
}

type caddyMatch struct {
	Host []string `json:"host"`
}

type caddyHandler struct {
	Handler    string              `json:"handler"`
	Upstreams  []caddyUpstream     `json:"upstreams,omitempty"`
	StatusCode int                 `json:"status_code,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Body       string              `json:"body,omitempty"`
}

type caddyUpstream struct {
	Dial string `json:"dial"`
}

type caddyTLSApp struct {
	Automation caddyAutomation `json:"automation"`
}

type caddyAutomation struct {
	Policies []caddyPolicy `json:"policies"`
}

type caddyPolicy struct {
	Subjects []string `json:"subjects"`
}

// entry é um host já resolvido: ou faz proxy, ou redireciona.
type entry struct {
	host       string
	upstream   string // vazio quando é só redirecionamento
	redirectTo string // host principal, quando é secundário do www
	forceHTTPS bool
}

// BuildConfig monta a configuração completa do Caddy para as rotas. O
// resultado é determinístico: os hosts saem ordenados por nome.
func BuildConfig(routes []Route) ([]byte, error) {
	entries, err := expand(routes)
	if err != nil {
		return nil, err
	}

	httpsRoutes := make([]caddyRoute, 0, len(entries)+1)
	httpRoutes := make([]caddyRoute, 0, len(entries)+1)
	subjects := make([]string, 0, len(entries))
	for _, e := range entries {
		subjects = append(subjects, e.host)
		if e.redirectTo != "" {
			httpsRoutes = append(httpsRoutes, route(e.host, redirect("https://"+e.redirectTo+"{http.request.uri}")))
			scheme := "http://"
			if e.forceHTTPS {
				scheme = "https://"
			}
			httpRoutes = append(httpRoutes, route(e.host, redirect(scheme+e.redirectTo+"{http.request.uri}")))
			continue
		}
		httpsRoutes = append(httpsRoutes, route(e.host, reverseProxy(e.upstream)))
		if e.forceHTTPS {
			httpRoutes = append(httpRoutes, route(e.host, redirect(forceHTTPSURL)))
		} else {
			httpRoutes = append(httpRoutes, route(e.host, reverseProxy(e.upstream)))
		}
	}

	httpsRoutes = append(httpsRoutes, catchAll())
	httpRoutes = append(httpRoutes, catchAll())

	cfg := caddyConfig{
		Admin: caddyAdmin{Listen: AdminListen},
		Apps: caddyApps{
			HTTP: caddyHTTPApp{Servers: map[string]caddyServer{
				httpsServerName: {
					Listen:         []string{":443"},
					AutomaticHTTPS: &caddyAutoHTTPS{DisableRedirects: true},
					Routes:         httpsRoutes,
				},
				httpServerName: {
					Listen: []string{":80"},
					Routes: httpRoutes,
				},
			}},
		},
	}
	if len(subjects) > 0 {
		cfg.Apps.TLS = &caddyTLSApp{Automation: caddyAutomation{
			Policies: []caddyPolicy{{Subjects: subjects}},
		}}
	}
	return json.Marshal(cfg)
}

// expand valida as rotas, acrescenta os hosts secundários do www e ordena.
func expand(routes []Route) ([]entry, error) {
	entries := make([]entry, 0, len(routes)*2)
	seen := make(map[string]bool, len(routes)*2)
	add := func(e entry) error {
		if seen[e.host] {
			return fmt.Errorf("host repetido: %s", e.host)
		}
		seen[e.host] = true
		entries = append(entries, e)
		return nil
	}

	for _, r := range routes {
		host := normalizeHost(r.Host)
		if host == "" {
			return nil, fmt.Errorf("rota sem host")
		}
		if strings.ContainsAny(host, ":/ ") {
			return nil, fmt.Errorf("host inválido: %q", r.Host)
		}
		if strings.TrimSpace(r.Upstream) == "" {
			return nil, fmt.Errorf("rota %s sem upstream", host)
		}

		var secondary string
		switch r.WWWRedirect {
		case "", WWWNone:
		case WWWToApex:
			if strings.HasPrefix(host, "www.") {
				return nil, fmt.Errorf("www_to_apex exige o domínio sem www como principal: %s", host)
			}
			secondary = "www." + host
		case WWWApexToWWW:
			if !strings.HasPrefix(host, "www.") || len(host) == len("www.") {
				return nil, fmt.Errorf("apex_to_www exige o domínio com www como principal: %s", host)
			}
			secondary = strings.TrimPrefix(host, "www.")
		default:
			return nil, fmt.Errorf("redirecionamento www desconhecido: %q", r.WWWRedirect)
		}

		if err := add(entry{host: host, upstream: strings.TrimSpace(r.Upstream), forceHTTPS: r.ForceHTTPS}); err != nil {
			return nil, err
		}
		if secondary != "" {
			if err := add(entry{host: secondary, redirectTo: host, forceHTTPS: r.ForceHTTPS}); err != nil {
				return nil, err
			}
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].host < entries[j].host })
	return entries, nil
}

func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

func route(host string, h caddyHandler) caddyRoute {
	return caddyRoute{
		Match:    []caddyMatch{{Host: []string{host}}},
		Handle:   []caddyHandler{h},
		Terminal: true,
	}
}

// catchAll é a rota final, sem matcher: 404 para hosts não cadastrados.
func catchAll() caddyRoute {
	return caddyRoute{
		Handle:   []caddyHandler{{Handler: "static_response", StatusCode: 404, Body: notConfiguredBody}},
		Terminal: true,
	}
}

func reverseProxy(upstream string) caddyHandler {
	return caddyHandler{Handler: "reverse_proxy", Upstreams: []caddyUpstream{{Dial: upstream}}}
}

func redirect(location string) caddyHandler {
	return caddyHandler{
		Handler:    "static_response",
		StatusCode: 308,
		Headers:    map[string][]string{"Location": {location}},
	}
}

// SameConfig diz se duas configs JSON são equivalentes, ignorando espaços e a
// ordem das chaves. JSON inválido nunca é igual a nada; "null" e vazio só são
// iguais entre si.
func SameConfig(a, b []byte) bool {
	na, okA := normalize(a)
	nb, okB := normalize(b)
	if !okA || !okB {
		return false
	}
	return bytes.Equal(na, nb)
}

func normalize(b []byte) ([]byte, bool) {
	if len(bytes.TrimSpace(b)) == 0 {
		return []byte("null"), true
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, false
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	return out, true
}
