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
//     de caminho casa com match.path "<prefixo>*", é terminal e sai do
//     prefixo mais longo para o mais curto. Antes de cada prefixo "/x/" vai
//     uma rota exata "/x" com 308 para "/x/{http.request.uri.prefixed_query}"
//     (o nginx responde 301 nesse caso para "location /x/"). A rota "/" vai
//     por último e sem matcher, como na v0.4.0 (pega também OPTIONS *).
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
//     O delete inclui Via, que o reverse_proxy põe no lugar do Server
//     ("1.1 Caddy"). Os valores de response.set saem escapados (ver
//     literal), para que chaves não virem placeholders.
//
//   - Mudanças adiadas não valem quando um erro sobe pela cadeia (doc do
//     handler headers): o 502 de upstream fora e o 413 do request_body. Por
//     isso cada servidor tem errors.routes: uma rota por host com cabeçalhos
//     próprios (strip + set do domínio) e uma final sem matcher (só strip),
//     todas respondendo static_response com status "{http.error.status_code}".
//     https://caddyserver.com/docs/json/apps/http/servers/errors/
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
//
// Pastas estáticas, certificado próprio, só Cloudflare e ensaio (verificado
// em 2026-11-09, Caddy 2.11; as formas batem com "caddy adapt" do
// Caddyfile equivalente):
//
//   - Rota estática: um subroute com file_server e root. Fora de "/", a
//     rota tem semântica de alias, como o handle_path do Caddyfile: um
//     rewrite strip_path_prefix "/app" vem primeiro, então /app/x sai de
//     root/x e immutablePrefix e o fallback são relativos à rota. O
//     file_server esconde .git e .env* (hide; .well-known continua servido).
//     Com immutablePrefix, uma rota
//     terminal "<prefixo>*" põe Cache-Control imutável e serve o arquivo
//     (arquivo ausente é 404, como "try_files $uri =404"). Com spa, um
//     matcher file com try_files ["{http.request.uri.path}", "/index.html"]
//     (o {path} do Caddyfile) reescreve para {http.matchers.file.relative};
//     depois da reescrita, path ["/", "/index.html"] ganha no-cache, então o
//     fallback também. Os Cache-Control são adiados: num erro (404) não saem.
//     https://caddyserver.com/docs/json/apps/http/servers/routes/handle/file_server/
//     https://caddyserver.com/docs/json/apps/http/servers/routes/match/file/
//     https://caddyserver.com/docs/json/apps/http/servers/routes/handle/rewrite/
//     https://caddyserver.com/docs/caddyfile/directives/try_files
//     https://caddyserver.com/docs/caddyfile/directives/handle_path
//     https://caddyserver.com/docs/json/apps/http/servers/routes/handle/rewrite/strip_path_prefix/
//     https://caddyserver.com/docs/json/apps/http/servers/routes/handle/file_server/hide/
//
//   - Certificado próprio: CertPEM é dividido em cadeia e chave e vai em
//     apps.tls.certificates.load_pem (um por conteúdo). Esses hosts saem das
//     políticas de automação e entram em automatic_https.skip_certificates do
//     servidor HTTPS, para o Caddy nunca pedir ACME por eles (autohttps.go
//     também pula nomes com certificado carregado).
//     https://caddyserver.com/docs/json/apps/tls/certificates/load_pem/
//     https://caddyserver.com/docs/json/apps/http/servers/automatic_https/skip_certificates/
//
//   - Só Cloudflare: antes das rotas do host vai, nos dois servidores, uma
//     rota host + not remote_ip (faixas CF) com static_response 403. O
//     remote_ip olha o par TCP (r.RemoteAddr), não o IP do cliente; quem lê
//     o IP do cliente é o client_ip (modules/caddyhttp/ip_matchers.go,
//     MatchRemoteIP e MatchClientIP). Os servidores ganham trusted_proxies
//     (source static, faixas CF) e client_ip_headers ["CF-Connecting-IP"];
//     o X-Real-IP desses sites vira {http.vars.client_ip}.
//     https://caddyserver.com/docs/json/apps/http/servers/routes/match/remote_ip/
//     https://caddyserver.com/docs/json/apps/http/servers/trusted_proxies/static/
//     https://caddyserver.com/docs/json/apps/http/servers/client_ip_headers/
//     https://github.com/caddyserver/caddy/blob/v2.11.0/modules/caddyhttp/ip_matchers.go
//
//   - Ensaio: escuta 127.0.0.1:18080 e 127.0.0.1:18443, com http_port e
//     https_port iguais, senão o Caddy trata o servidor de 18080 como HTTPS
//     (autohttps.go, listenersUseAnyPortOtherThan(app.httpPort())). Os
//     sites só Cloudflare aceitam também 127.0.0.1/32 e ::1/128. A política
//     dos hosts auto usa o emissor internal: enquanto outro processo tem a
//     :80, um pedido ACME só falharia (e gastaria o limite de falhas do
//     Let's Encrypt).
//     https://caddyserver.com/docs/json/apps/tls/automation/policies/issuers/internal/
//     https://caddyserver.com/docs/json/apps/http/http_port/

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/netip"
	"path"
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
	// errorStatus é o status recomendado do erro, nas rotas de erro.
	errorStatus = "{http.error.status_code}"
	// keepQuery acrescenta "?query" quando há query string.
	keepQuery = "{http.request.uri.prefixed_query}"
	// clientIPValue é o X-Real-IP dos sites só Cloudflare (IP do cliente,
	// lido de CF-Connecting-IP quando o par é da Cloudflare).
	clientIPValue = "{http.vars.client_ip}"
	// cfHeader é o cabeçalho com o IP do cliente enviado pela Cloudflare.
	cfHeader = "CF-Connecting-IP"
	// staticRoot é a pasta que o Caddy vê (bind mount somente leitura).
	staticRoot = "/var/www"
	// Cache-Control das pastas estáticas.
	cacheNoCache   = "no-cache"
	cacheImmutable = "public, max-age=31536000, immutable"
	// spaFallback é o arquivo servido para caminhos desconhecidos com spa.
	spaFallback = "/index.html"
)

// Portas do modo ensaio.
const (
	trialHTTPPort  = 18080
	trialHTTPSPort = 18443
)

// hiddenFiles são escondidos pelo file_server (404): .git e .env* em
// qualquer nível. Não escondemos todo arquivo com ponto por causa de
// .well-known.
var hiddenFiles = []string{".git", ".env*"}

// loopbackRanges são aceitos nos sites só Cloudflare durante o ensaio.
var loopbackRanges = []string{"127.0.0.1/32", "::1/128"}

// Limites por domínio.
const (
	MaxRoutes  = 20
	MaxHeaders = 30
)

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
	HTTPPort  int                    `json:"http_port,omitempty"`
	HTTPSPort int                    `json:"https_port,omitempty"`
	Servers   map[string]caddyServer `json:"servers"`
}

type caddyServer struct {
	Listen          []string             `json:"listen"`
	AutomaticHTTPS  *caddyAutoHTTPS      `json:"automatic_https,omitempty"`
	Routes          []caddyRoute         `json:"routes"`
	Errors          *caddyErrors         `json:"errors,omitempty"`
	TrustedProxies  *caddyTrustedProxies `json:"trusted_proxies,omitempty"`
	ClientIPHeaders []string             `json:"client_ip_headers,omitempty"`
}

type caddyTrustedProxies struct {
	Source string   `json:"source"`
	Ranges []string `json:"ranges"`
}

type caddyErrors struct {
	Routes []caddyRoute `json:"routes"`
}

type caddyAutoHTTPS struct {
	DisableRedirects bool     `json:"disable_redirects,omitempty"`
	SkipCerts        []string `json:"skip_certificates,omitempty"`
}

type caddyRoute struct {
	Match    []caddyMatch   `json:"match,omitempty"`
	Handle   []caddyHandler `json:"handle"`
	Terminal bool           `json:"terminal"`
}

type caddyMatch struct {
	Host     []string        `json:"host,omitempty"`
	Path     []string        `json:"path,omitempty"`
	File     *caddyFileMatch `json:"file,omitempty"`
	RemoteIP *caddyIPRanges  `json:"remote_ip,omitempty"`
	Not      []caddyMatch    `json:"not,omitempty"`
}

type caddyFileMatch struct {
	Root     string   `json:"root,omitempty"`
	TryFiles []string `json:"try_files"`
}

type caddyIPRanges struct {
	Ranges []string `json:"ranges"`
}

// caddyHandler cobre os handlers usados. Headers muda de forma conforme o
// handler: map[string][]string no static_response, *caddyProxyHeaders no
// reverse_proxy.
type caddyHandler struct {
	Handler       string              `json:"handler"`
	Upstreams     []caddyUpstream     `json:"upstreams,omitempty"`
	FlushInterval *int                `json:"flush_interval,omitempty"`
	StatusCode    any                 `json:"status_code,omitempty"` // int ou placeholder
	Headers       any                 `json:"headers,omitempty"`
	Body          string              `json:"body,omitempty"`
	Response      *caddyRespHeaderOps `json:"response,omitempty"`
	Encodings     map[string]struct{} `json:"encodings,omitempty"`
	Prefer        []string            `json:"prefer,omitempty"`
	MaxSize       int64               `json:"max_size,omitempty"`
	Routes        []caddyRoute        `json:"routes,omitempty"`
	Root          string              `json:"root,omitempty"`
	Hide          []string            `json:"hide,omitempty"`
	URI           string              `json:"uri,omitempty"`
	StripPrefix   string              `json:"strip_path_prefix,omitempty"`
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
	Certificates *caddyCertificates `json:"certificates,omitempty"`
	Automation   *caddyAutomation   `json:"automation,omitempty"`
}

type caddyCertificates struct {
	LoadPEM []caddyPEM `json:"load_pem"`
}

type caddyPEM struct {
	Certificate string `json:"certificate"`
	Key         string `json:"key"`
}

type caddyAutomation struct {
	Policies []caddyPolicy `json:"policies"`
}

type caddyPolicy struct {
	Subjects []string      `json:"subjects"`
	Issuers  []caddyIssuer `json:"issuers,omitempty"`
}

type caddyIssuer struct {
	Module string `json:"module"`
}

// entry é um host já resolvido: ou serve o site, ou redireciona.
type entry struct {
	host       string
	site       *Site  // nil quando é só redirecionamento
	redirectTo string // host principal, quando é secundário do www
	forceHTTPS bool
	cfOnly     bool      // só Cloudflare (vale também para o secundário)
	cert       *caddyPEM // certificado próprio (também no secundário)
}

// BuildConfig monta a configuração completa do Caddy para os sites. O
// resultado é determinístico: os hosts saem ordenados por nome e as rotas de
// caminho do prefixo mais longo para o mais curto. cf são as faixas da
// Cloudflare em CIDR.
func BuildConfig(sites []Site, mode Listen, cf []string) ([]byte, error) {
	switch mode {
	case ListenNormal, ListenTrial, "":
	default:
		return nil, fmt.Errorf("modo de escuta desconhecido: %q", mode)
	}
	entries, err := expand(sites)
	if err != nil {
		return nil, err
	}

	// faixas aceitas nos sites só Cloudflare; só são validadas quando algum
	// site as usa, para uma faixa ruim no cache não derrubar a config toda
	anyCF := false
	for _, e := range entries {
		anyCF = anyCF || e.cfOnly
	}
	var allow, ranges []string
	if anyCF {
		if ranges, err = checkRanges(cf); err != nil {
			return nil, err
		}
		if len(ranges) == 0 {
			return nil, fmt.Errorf("site só Cloudflare sem faixas da Cloudflare")
		}
		allow = append(allow, ranges...)
		if mode == ListenTrial {
			allow = append(allow, loopbackRanges...)
		}
	}

	httpsRoutes := make([]caddyRoute, 0, len(entries)+1)
	httpRoutes := make([]caddyRoute, 0, len(entries)+1)
	var httpsErrors, httpErrors []caddyRoute
	var subjects, skip []string
	var certs []caddyPEM
	seenCert := map[caddyPEM]bool{}
	for _, e := range entries {
		if e.cert != nil {
			skip = append(skip, e.host)
			if !seenCert[*e.cert] {
				seenCert[*e.cert] = true
				certs = append(certs, *e.cert)
			}
		} else {
			subjects = append(subjects, e.host)
		}
		if e.cfOnly {
			httpsRoutes = append(httpsRoutes, denyRoute(e.host, allow))
			httpRoutes = append(httpRoutes, denyRoute(e.host, allow))
		}
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
		errRoute := len(e.site.Headers) > 0
		if errRoute {
			httpsErrors = append(httpsErrors, hostRoute(e.host, responseHeaders(e.site), errorResponse()))
		}
		if e.forceHTTPS {
			httpRoutes = append(httpRoutes, hostRoute(e.host, stripServer(), redirect(forceHTTPSURL)))
		} else {
			httpRoutes = append(httpRoutes, hostRoute(e.host, siteHandlers(e.site)...))
			if errRoute {
				httpErrors = append(httpErrors, hostRoute(e.host, responseHeaders(e.site), errorResponse()))
			}
		}
	}

	httpsRoutes = append(httpsRoutes, catchAll())
	httpRoutes = append(httpRoutes, catchAll())
	httpsErrors = append(httpsErrors, errorCatchAll())
	httpErrors = append(httpErrors, errorCatchAll())

	httpsSrv := caddyServer{
		Listen:         []string{":443"},
		AutomaticHTTPS: &caddyAutoHTTPS{DisableRedirects: true, SkipCerts: skip},
		Routes:         httpsRoutes,
		Errors:         &caddyErrors{Routes: httpsErrors},
	}
	httpSrv := caddyServer{
		Listen: []string{":80"},
		Routes: httpRoutes,
		Errors: &caddyErrors{Routes: httpErrors},
	}
	if anyCF {
		for _, srv := range []*caddyServer{&httpsSrv, &httpSrv} {
			srv.TrustedProxies = &caddyTrustedProxies{Source: "static", Ranges: ranges}
			srv.ClientIPHeaders = []string{cfHeader}
		}
	}
	var app caddyHTTPApp
	if mode == ListenTrial {
		httpsSrv.Listen = []string{fmt.Sprintf("127.0.0.1:%d", trialHTTPSPort)}
		httpSrv.Listen = []string{fmt.Sprintf("127.0.0.1:%d", trialHTTPPort)}
		app.HTTPPort, app.HTTPSPort = trialHTTPPort, trialHTTPSPort
	}
	app.Servers = map[string]caddyServer{httpsServerName: httpsSrv, httpServerName: httpSrv}

	cfg := caddyConfig{Admin: caddyAdmin{Listen: AdminListen}, Apps: caddyApps{HTTP: app}}
	if len(subjects) > 0 || len(certs) > 0 {
		cfg.Apps.TLS = &caddyTLSApp{}
		if len(certs) > 0 {
			cfg.Apps.TLS.Certificates = &caddyCertificates{LoadPEM: certs}
		}
		if len(subjects) > 0 {
			pol := caddyPolicy{Subjects: subjects}
			if mode == ListenTrial {
				pol.Issuers = []caddyIssuer{{Module: "internal"}}
			}
			cfg.Apps.TLS.Automation = &caddyAutomation{Policies: []caddyPolicy{pol}}
		}
	}
	return json.Marshal(cfg)
}

// checkRanges valida as faixas da Cloudflare (CIDR) e devolve a forma
// canônica, na mesma ordem.
func checkRanges(cf []string) ([]string, error) {
	out := make([]string, 0, len(cf))
	for _, c := range cf {
		p, err := netip.ParsePrefix(strings.TrimSpace(c))
		if err != nil {
			return nil, fmt.Errorf("faixa da Cloudflare inválida: %q", c)
		}
		out = append(out, p.String())
	}
	return out, nil
}

// denyRoute responde 403 a quem acessa host de fora das faixas permitidas.
// remote_ip olha o par TCP, não o IP do cliente (ver o comentário do topo).
func denyRoute(host string, allow []string) caddyRoute {
	return caddyRoute{
		Match: []caddyMatch{{
			Host: []string{host},
			Not:  []caddyMatch{{RemoteIP: &caddyIPRanges{Ranges: allow}}},
		}},
		Handle:   []caddyHandler{stripServer(), {Handler: "static_response", StatusCode: 403}},
		Terminal: true,
	}
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
		if strings.ContainsAny(host, ":/ {}") {
			return nil, fmt.Errorf("host inválido: %q", s.Host)
		}
		site, err := checkSite(host, s)
		if err != nil {
			return nil, err
		}
		cert, err := siteCert(host, s.TLS)
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

		if err := add(entry{host: host, site: site, forceHTTPS: s.ForceHTTPS, cfOnly: s.CloudflareOnly, cert: cert}); err != nil {
			return nil, err
		}
		if secondary != "" {
			if err := add(entry{host: secondary, redirectTo: host, forceHTTPS: s.ForceHTTPS, cfOnly: s.CloudflareOnly, cert: cert}); err != nil {
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
			if strings.TrimSpace(r.Upstream) != "" {
				return nil, fmt.Errorf("%s%s: rota com upstream e pasta estática", host, r.Path)
			}
			if err := checkStatic(r.Static); err != nil {
				return nil, fmt.Errorf("%s%s: %w", host, r.Path, err)
			}
			continue
		}
		if strings.TrimSpace(r.Upstream) == "" {
			return nil, fmt.Errorf("%s%s: rota sem upstream", host, r.Path)
		}
		if strings.ContainsAny(r.Upstream, "{}") {
			return nil, fmt.Errorf("%s%s: upstream inválido: %q", host, r.Path, r.Upstream)
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
	for i, r := range out.Routes {
		if r.Static != nil {
			st := *r.Static
			st.Root = path.Clean(st.Root)
			out.Routes[i].Static = &st
		}
	}
	sort.Slice(out.Routes, func(i, j int) bool {
		a, b := out.Routes[i].Path, out.Routes[j].Path
		if len(a) != len(b) {
			return len(a) > len(b)
		}
		return a < b
	})
	return &out, nil
}

// checkStatic valida a pasta de uma rota estática: root absoluto que, depois
// de limpo, é /var/www ou fica dentro dele; immutablePrefix vazio ou um
// caminho "/x/" (relativo à rota). Os caminhos são do Linux (o Caddy roda
// lá), por isso path e não filepath.
func checkStatic(st *StaticTarget) error {
	root := st.Root
	if !strings.HasPrefix(root, "/") {
		return fmt.Errorf("pasta estática precisa de caminho absoluto: %q", root)
	}
	clean := path.Clean(root)
	if clean != staticRoot && !strings.HasPrefix(clean, staticRoot+"/") {
		return fmt.Errorf("pasta estática fora de %s: %q", staticRoot, root)
	}
	if p := st.ImmutablePrefix; p != "" {
		if p == "/" || !validPath(p) {
			return fmt.Errorf("prefixo imutável inválido: %q", p)
		}
	}
	return nil
}

// siteCert divide o CertPEM de um site custom em cadeia e chave (por bloco
// PEM, na ordem em que aparecem) e confere que a chave é do certificado.
// Sites auto devolvem nil.
func siteCert(host string, t SiteTLS) (*caddyPEM, error) {
	switch t.Mode {
	case "", TLSAuto:
		return nil, nil
	case TLSCustom:
	default:
		return nil, fmt.Errorf("%s: modo de certificado desconhecido: %q", host, t.Mode)
	}
	var chain, key []byte
	keys := 0
	rest := []byte(t.CertPEM)
	for {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		switch {
		case b.Type == "CERTIFICATE":
			chain = append(chain, pem.EncodeToMemory(b)...)
		case strings.HasSuffix(b.Type, "PRIVATE KEY"):
			key = pem.EncodeToMemory(b)
			keys++
		}
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("%s: certificado próprio sem certificado", host)
	}
	if keys != 1 {
		return nil, fmt.Errorf("%s: certificado próprio precisa de exatamente uma chave (há %d)", host, keys)
	}
	if _, err := tls.X509KeyPair(chain, key); err != nil {
		return nil, fmt.Errorf("%s: certificado próprio inválido: %v", host, err)
	}
	return &caddyPEM{Certificate: string(chain), Key: string(key)}, nil
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
// encode (com Compression) e o subroute com as rotas de caminho. Cada
// prefixo "/x/" ganha antes dele uma rota exata "/x" com 308 para "/x/"
// (como o "location /x/" do nginx); a rota "/" vai por último e sem matcher,
// como na v0.4.0, para pegar também OPTIONS * e afins.
func siteHandlers(s *Site) []caddyHandler {
	out := []caddyHandler{responseHeaders(s)}
	if s.Compression {
		out = append(out, caddyHandler{
			Handler:   "encode",
			Encodings: map[string]struct{}{"zstd": {}, "gzip": {}},
			Prefer:    []string{"zstd", "gzip"},
		})
	}
	routes := make([]caddyRoute, 0, 2*len(s.Routes))
	for _, r := range s.Routes {
		if r.Path == "/" {
			routes = append(routes, caddyRoute{Handle: pathHandlers(r, s.CloudflareOnly), Terminal: true})
			continue
		}
		routes = append(routes,
			caddyRoute{
				Match:    []caddyMatch{{Path: []string{strings.TrimSuffix(r.Path, "/")}}},
				Handle:   []caddyHandler{redirect(r.Path + keepQuery)},
				Terminal: true,
			},
			caddyRoute{
				Match:    []caddyMatch{{Path: []string{r.Path + "*"}}},
				Handle:   pathHandlers(r, s.CloudflareOnly),
				Terminal: true,
			})
	}
	return append(out, caddyHandler{Handler: "subroute", Routes: routes})
}

// responseHeaders remove Server e Via e aplica os cabeçalhos do domínio, com
// os valores escapados (ver literal).
func responseHeaders(s *Site) caddyHandler {
	hdr := stripServer()
	if len(s.Headers) > 0 {
		hdr.Response.Set = make(map[string][]string, len(s.Headers))
		for _, h := range s.Headers {
			hdr.Response.Set[h.Name] = []string{literal(h.Value)}
		}
	}
	return hdr
}

// literal escapa as chaves de v para que o Caddy não as leia como
// placeholder: o replacer trata \{ e \} como { e } literais (caddy
// replacer.go, phEscape; o handler headers passa os valores por
// repl.ReplaceKnown). Sem isso, um valor "{env.SEGREDO}" sairia expandido
// em toda resposta. Uma barra antes de chave também sai certa: `a\{` vira
// `a\\{`, que o replacer devolve como `a\{` (ele só consome a barra
// imediatamente antes da chave).
// https://github.com/caddyserver/caddy/blob/v2.11.0/replacer.go
// https://caddyserver.com/docs/conventions#placeholders
func literal(v string) string {
	return strings.NewReplacer("{", `\{`, "}", `\}`).Replace(v)
}

// pathHandlers são os handlers de uma rota de caminho com upstream:
// request_body (com MaxBodyBytes) e reverse_proxy.
func pathHandlers(r SiteRoute, cfOnly bool) []caddyHandler {
	if r.Static != nil {
		return []caddyHandler{staticHandler(r.Path, r.Static)}
	}
	realIP := realIPValue
	if cfOnly {
		realIP = clientIPValue
	}
	var out []caddyHandler
	if r.MaxBodyBytes > 0 {
		out = append(out, caddyHandler{Handler: "request_body", MaxSize: r.MaxBodyBytes})
	}
	rp := caddyHandler{
		Handler:   "reverse_proxy",
		Upstreams: []caddyUpstream{{Dial: strings.TrimSpace(r.Upstream)}},
		Headers: &caddyProxyHeaders{Request: &caddyHeaderOps{
			Set: map[string][]string{"X-Real-IP": {realIP}},
		}},
	}
	if r.NoBuffering {
		v := -1
		rp.FlushInterval = &v
	}
	return append(out, rp)
}

// staticHandler serve a pasta. Fora de "/", tira antes o prefixo da rota
// (alias, como handle_path), e o resto vale sobre o caminho já cortado:
// com immutablePrefix, uma rota terminal com
// cache imutável (arquivo ausente é 404, sem fallback); com spa, try_files
// {path} /index.html e no-cache em "/" e "/index.html" depois da reescrita
// (pega o fallback e o pedido direto); por fim, o file_server. root e o
// prefixo saem escapados (ver literal).
func staticHandler(routePath string, st *StaticTarget) caddyHandler {
	root := literal(st.Root)
	fs := caddyHandler{Handler: "file_server", Root: root, Hide: hiddenFiles}
	var routes []caddyRoute
	if routePath != "/" {
		routes = append(routes, caddyRoute{Handle: []caddyHandler{{
			Handler: "rewrite", StripPrefix: strings.TrimSuffix(routePath, "/"),
		}}})
	}
	if st.ImmutablePrefix != "" {
		routes = append(routes, caddyRoute{
			Match:    []caddyMatch{{Path: []string{literal(st.ImmutablePrefix) + "*"}}},
			Handle:   []caddyHandler{cacheControl(cacheImmutable), fs},
			Terminal: true,
		})
	}
	if st.SPA {
		routes = append(routes,
			caddyRoute{
				Match: []caddyMatch{{File: &caddyFileMatch{
					Root:     root,
					TryFiles: []string{"{http.request.uri.path}", spaFallback},
				}}},
				Handle: []caddyHandler{{Handler: "rewrite", URI: "{http.matchers.file.relative}"}},
			},
			caddyRoute{
				Match:  []caddyMatch{{Path: []string{"/", spaFallback}}},
				Handle: []caddyHandler{cacheControl(cacheNoCache)},
			})
	}
	routes = append(routes, caddyRoute{Handle: []caddyHandler{fs}, Terminal: true})
	return caddyHandler{Handler: "subroute", Routes: routes}
}

// cacheControl define Cache-Control adiado: só vale se a resposta for
// escrita pela cadeia normal (um 404 do file_server não leva o cache).
func cacheControl(v string) caddyHandler {
	return caddyHandler{Handler: "headers", Response: &caddyRespHeaderOps{
		Set: map[string][]string{"Cache-Control": {v}}, Deferred: true,
	}}
}

// stripServer remove Server e Via da resposta (adiado; ver o comentário do
// topo). O Via ("1.1 Caddy") é o que o reverse_proxy põe no lugar do Server.
func stripServer() caddyHandler {
	return caddyHandler{Handler: "headers", Response: &caddyRespHeaderOps{
		Delete: []string{"Server", "Via"}, Deferred: true,
	}}
}

// errorResponse responde o status recomendado do erro, sem corpo (como o
// Caddy faz sem rotas de erro).
func errorResponse() caddyHandler {
	return caddyHandler{Handler: "static_response", StatusCode: errorStatus}
}

// errorCatchAll é a rota de erro final, sem matcher: hosts sem cabeçalhos
// próprios (ou não cadastrados) só perdem Server e Via.
func errorCatchAll() caddyRoute {
	return caddyRoute{Handle: []caddyHandler{stripServer(), errorResponse()}, Terminal: true}
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
