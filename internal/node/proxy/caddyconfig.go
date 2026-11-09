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
//     -Host por padrão; acrescentamos só X-Real-IP (headers.request.set) com
//     {http.request.remote.host}, como o nginx costuma enviar.
//
// Rotas por caminho, cabeçalhos e compressão (verificado em 2026-11-09,
// Caddy 2.11):
//
//   - Cada rota de host tem, nesta ordem: um handler "headers", um "encode"
//     (só com Compression) e um "subroute" com as rotas de caminho. Cada rota
//     de caminho casa com match.path "<prefixo>*" ("/" vira "/*"), é terminal
//     e sai do prefixo mais longo para o mais curto, com "/" por último.
//     https://caddyserver.com/docs/json/apps/http/servers/routes/handle/subroute/
//     https://caddyserver.com/docs/json/apps/http/servers/routes/match/path/
//
//   - O cabeçalho Server é removido com response.delete ["Server"] e
//     deferred: true. O Caddy grava "Server: Caddy" antes de rodar as rotas
//     (modules/caddyhttp/server.go, h["Server"] = serverHeader), e o
//     reverse_proxy apaga esse valor e copia os cabeçalhos da resposta do
//     upstream, inclusive o Server dele (reverseproxy.go: "delete our Server
//     header and use Via instead", seguido de copyHeader). Um delete imediato
//     só tiraria o do Caddy; o adiado roda quando a resposta é escrita e tira
//     os dois. A doc do campo diz: "Usually you will need to set this to true
//     if any fields are being deleted" (headers.RespHeaderOps), e o Caddyfile
//     adia sozinho quando há "-Campo".
//     https://caddyserver.com/docs/caddyfile/directives/header
//     https://caddyserver.com/docs/json/apps/http/servers/routes/handle/headers/response/deferred/
//     https://github.com/caddyserver/caddy/blob/v2.11.0/modules/caddyhttp/headers/headers.go
//     https://github.com/caddyserver/caddy/blob/v2.11.0/modules/caddyhttp/reverseproxy/reverseproxy.go
//     Os cabeçalhos do domínio vão no mesmo handler, em response.set, e por
//     isso também são adiados: substituem o valor que o upstream mandar.
//     Ressalva da doc do handler: mudanças adiadas não valem se um erro sobe
//     pela cadeia (por exemplo, o 502 que o próprio Caddy gera).
//
//   - Compressão: handler "encode" com encodings zstd e gzip e prefer
//     ["zstd", "gzip"] (o mesmo que "encode zstd gzip" no Caddyfile).
//     https://caddyserver.com/docs/json/apps/http/servers/routes/handle/encode/
//
//   - maxBodyBytes > 0 põe um "request_body" com max_size antes do
//     reverse_proxy (acima do limite, 413). noBuffering é flush_interval -1
//     ("a negative value disables buffering").
//     https://caddyserver.com/docs/json/apps/http/servers/routes/handle/request_body/
//     https://caddyserver.com/docs/json/apps/http/servers/routes/handle/reverse_proxy/flush_interval/

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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
	// realIPValue é o X-Real-IP enviado ao upstream.
	realIPValue = "{http.request.remote.host}"
)

// Limites por domínio.
const (
	MaxRoutes  = 20
	MaxHeaders = 30
)

// errNotYet marca recursos que a configuração ainda não gera.
var errNotYet = errors.New("não suportado ainda")

var (
	headerNameRe  = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]+$")
	pathSegmentRe = regexp.MustCompile(`^[a-z0-9._~-]+$`)
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
	Host []string `json:"host,omitempty"`
	Path []string `json:"path,omitempty"`
}

// caddyHandler cobre os handlers usados. Headers muda de forma conforme o
// handler: map[string][]string no static_response, *caddyProxyHeaders no
// reverse_proxy.
type caddyHandler struct {
	Handler       string              `json:"handler"`
	Upstreams     []caddyUpstream     `json:"upstreams,omitempty"`
	FlushInterval *int                `json:"flush_interval,omitempty"`
	StatusCode    int                 `json:"status_code,omitempty"`
	Headers       any                 `json:"headers,omitempty"`
	Body          string              `json:"body,omitempty"`
	Response      *caddyRespHeaderOps `json:"response,omitempty"`
	Encodings     map[string]struct{} `json:"encodings,omitempty"`
	Prefer        []string            `json:"prefer,omitempty"`
	MaxSize       int64               `json:"max_size,omitempty"`
	Routes        []caddyRoute        `json:"routes,omitempty"`
}

type caddyUpstream struct {
	Dial string `json:"dial"`
}

type caddyHeaderOps struct {
	Set map[string][]string `json:"set,omitempty"`
}

type caddyProxyHeaders struct {
	Request *caddyHeaderOps `json:"request,omitempty"`
}

type caddyRespHeaderOps struct {
	Set      map[string][]string `json:"set,omitempty"`
	Delete   []string            `json:"delete,omitempty"`
	Deferred bool                `json:"deferred,omitempty"`
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

// entry é um host já resolvido: ou serve o site, ou redireciona.
type entry struct {
	host       string
	site       *Site  // nil quando é só redirecionamento
	redirectTo string // host principal, quando é secundário do www
	forceHTTPS bool
}

// BuildConfig monta a configuração completa do Caddy para os sites. O
// resultado é determinístico: os hosts saem ordenados por nome e as rotas de
// caminho do prefixo mais longo para o mais curto. cf são as faixas da
// Cloudflare em CIDR.
func BuildConfig(sites []Site, mode Listen, cf []string) ([]byte, error) {
	switch mode {
	case ListenNormal, "":
	case ListenTrial:
		return nil, fmt.Errorf("modo ensaio: %w", errNotYet)
	default:
		return nil, fmt.Errorf("modo de escuta desconhecido: %q", mode)
	}
	if len(cf) > 0 {
		return nil, fmt.Errorf("faixas da Cloudflare: %w", errNotYet)
	}
	entries, err := expand(sites)
	if err != nil {
		return nil, err
	}

	httpsRoutes := make([]caddyRoute, 0, len(entries)+1)
	httpRoutes := make([]caddyRoute, 0, len(entries)+1)
	subjects := make([]string, 0, len(entries))
	for _, e := range entries {
		subjects = append(subjects, e.host)
		if e.redirectTo != "" {
			httpsRoutes = append(httpsRoutes, hostRoute(e.host, stripServer(), redirect("https://"+e.redirectTo+"{http.request.uri}")))
			scheme := "http://"
			if e.forceHTTPS {
				scheme = "https://"
			}
			httpRoutes = append(httpRoutes, hostRoute(e.host, stripServer(), redirect(scheme+e.redirectTo+"{http.request.uri}")))
			continue
		}
		httpsRoutes = append(httpsRoutes, hostRoute(e.host, siteHandlers(e.site)...))
		if e.forceHTTPS {
			httpRoutes = append(httpRoutes, hostRoute(e.host, stripServer(), redirect(forceHTTPSURL)))
		} else {
			httpRoutes = append(httpRoutes, hostRoute(e.host, siteHandlers(e.site)...))
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

// expand valida os sites, acrescenta os hosts secundários do www e ordena.
func expand(sites []Site) ([]entry, error) {
	entries := make([]entry, 0, len(sites)*2)
	seen := make(map[string]bool, len(sites)*2)
	add := func(e entry) error {
		if seen[e.host] {
			return fmt.Errorf("host repetido: %s", e.host)
		}
		seen[e.host] = true
		entries = append(entries, e)
		return nil
	}

	for _, s := range sites {
		host := normalizeHost(s.Host)
		if host == "" {
			return nil, fmt.Errorf("site sem host")
		}
		if strings.ContainsAny(host, ":/ ") {
			return nil, fmt.Errorf("host inválido: %q", s.Host)
		}
		site, err := checkSite(host, s)
		if err != nil {
			return nil, err
		}

		var secondary string
		switch s.WWWRedirect {
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
			return nil, fmt.Errorf("redirecionamento www desconhecido: %q", s.WWWRedirect)
		}

		if err := add(entry{host: host, site: site, forceHTTPS: s.ForceHTTPS}); err != nil {
			return nil, err
		}
		if secondary != "" {
			if err := add(entry{host: secondary, redirectTo: host, forceHTTPS: s.ForceHTTPS}); err != nil {
				return nil, err
			}
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].host < entries[j].host })
	return entries, nil
}

// checkSite valida rotas, cabeçalhos e opções de um site e devolve uma cópia
// com as rotas ordenadas (prefixo mais longo primeiro, "/" por último). A
// entrada não é alterada.
func checkSite(host string, s Site) (*Site, error) {
	switch s.TLS.Mode {
	case "", TLSAuto:
	case TLSCustom:
		return nil, fmt.Errorf("%s: certificado próprio: %w", host, errNotYet)
	default:
		return nil, fmt.Errorf("%s: modo de certificado desconhecido: %q", host, s.TLS.Mode)
	}
	if s.CloudflareOnly {
		return nil, fmt.Errorf("%s: só Cloudflare: %w", host, errNotYet)
	}

	if len(s.Routes) == 0 {
		return nil, fmt.Errorf("%s: site sem rotas", host)
	}
	if len(s.Routes) > MaxRoutes {
		return nil, fmt.Errorf("%s: no máximo %d rotas", host, MaxRoutes)
	}
	paths := make(map[string]bool, len(s.Routes))
	for _, r := range s.Routes {
		if !validPath(r.Path) {
			return nil, fmt.Errorf("%s: caminho inválido: %q", host, r.Path)
		}
		if paths[r.Path] {
			return nil, fmt.Errorf("%s: caminho repetido: %s", host, r.Path)
		}
		paths[r.Path] = true
		if r.Static != nil {
			return nil, fmt.Errorf("%s%s: pasta estática: %w", host, r.Path, errNotYet)
		}
		if strings.TrimSpace(r.Upstream) == "" {
			return nil, fmt.Errorf("%s%s: rota sem upstream", host, r.Path)
		}
		if r.MaxBodyBytes < 0 {
			return nil, fmt.Errorf("%s%s: limite de corpo negativo", host, r.Path)
		}
	}
	if !paths["/"] {
		return nil, fmt.Errorf("%s: falta a rota /", host)
	}

	if len(s.Headers) > MaxHeaders {
		return nil, fmt.Errorf("%s: no máximo %d cabeçalhos", host, MaxHeaders)
	}
	names := make(map[string]bool, len(s.Headers))
	for _, h := range s.Headers {
		if !headerNameRe.MatchString(h.Name) {
			return nil, fmt.Errorf("%s: nome de cabeçalho inválido: %q", host, h.Name)
		}
		if strings.ContainsAny(h.Value, "\r\n") {
			return nil, fmt.Errorf("%s: valor do cabeçalho %s com quebra de linha", host, h.Name)
		}
		key := strings.ToLower(h.Name)
		if names[key] {
			return nil, fmt.Errorf("%s: cabeçalho repetido: %s", host, h.Name)
		}
		names[key] = true
	}

	out := s
	out.Routes = append([]SiteRoute(nil), s.Routes...)
	sort.Slice(out.Routes, func(i, j int) bool {
		a, b := out.Routes[i].Path, out.Routes[j].Path
		if len(a) != len(b) {
			return len(a) > len(b)
		}
		return a < b
	})
	return &out, nil
}

// validPath aceita "/" ou "/seg/.../" com segmentos [a-z0-9._~-].
func validPath(p string) bool {
	if p == "/" {
		return true
	}
	if len(p) < 3 || !strings.HasPrefix(p, "/") || !strings.HasSuffix(p, "/") {
		return false
	}
	for _, seg := range strings.Split(p[1:len(p)-1], "/") {
		if !pathSegmentRe.MatchString(seg) {
			return false
		}
	}
	return true
}

func normalizeHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

func hostRoute(host string, h ...caddyHandler) caddyRoute {
	return caddyRoute{
		Match:    []caddyMatch{{Host: []string{host}}},
		Handle:   h,
		Terminal: true,
	}
}

// siteHandlers são os handlers da rota de host que serve o site: headers,
// encode (com Compression) e o subroute com as rotas de caminho.
func siteHandlers(s *Site) []caddyHandler {
	hdr := stripServer()
	if len(s.Headers) > 0 {
		hdr.Response.Set = make(map[string][]string, len(s.Headers))
		for _, h := range s.Headers {
			hdr.Response.Set[h.Name] = []string{h.Value}
		}
	}
	out := []caddyHandler{hdr}
	if s.Compression {
		out = append(out, caddyHandler{
			Handler:   "encode",
			Encodings: map[string]struct{}{"zstd": {}, "gzip": {}},
			Prefer:    []string{"zstd", "gzip"},
		})
	}
	routes := make([]caddyRoute, 0, len(s.Routes))
	for _, r := range s.Routes {
		routes = append(routes, caddyRoute{
			Match:    []caddyMatch{{Path: []string{r.Path + "*"}}},
			Handle:   pathHandlers(r),
			Terminal: true,
		})
	}
	return append(out, caddyHandler{Handler: "subroute", Routes: routes})
}

// pathHandlers são os handlers de uma rota de caminho com upstream:
// request_body (com MaxBodyBytes) e reverse_proxy.
func pathHandlers(r SiteRoute) []caddyHandler {
	var out []caddyHandler
	if r.MaxBodyBytes > 0 {
		out = append(out, caddyHandler{Handler: "request_body", MaxSize: r.MaxBodyBytes})
	}
	rp := caddyHandler{
		Handler:   "reverse_proxy",
		Upstreams: []caddyUpstream{{Dial: strings.TrimSpace(r.Upstream)}},
		Headers: &caddyProxyHeaders{Request: &caddyHeaderOps{
			Set: map[string][]string{"X-Real-IP": {realIPValue}},
		}},
	}
	if r.NoBuffering {
		v := -1
		rp.FlushInterval = &v
	}
	return append(out, rp)
}

// stripServer remove o Server da resposta (adiado; ver o comentário do topo).
func stripServer() caddyHandler {
	return caddyHandler{Handler: "headers", Response: &caddyRespHeaderOps{
		Delete: []string{"Server"}, Deferred: true,
	}}
}

// catchAll é a rota final, sem matcher: 404 para hosts não cadastrados,
// também sem o cabeçalho Server.
func catchAll() caddyRoute {
	return caddyRoute{
		Handle:   []caddyHandler{stripServer(), {Handler: "static_response", StatusCode: 404, Body: notConfiguredBody}},
		Terminal: true,
	}
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
