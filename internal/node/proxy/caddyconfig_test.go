package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// simple monta um Site com uma única rota "/" para upstream (o formato da
// v0.4.0).
func simple(host, upstream string, force bool, www string) Site {
	return Site{Host: host, ForceHTTPS: force, WWWRedirect: www,
		Routes: []SiteRoute{{Path: "/", Upstream: upstream}}}
}

func threeSites() []Site {
	return []Site{
		simple("app.exemplo.com", "127.0.0.1:20001", true, "none"),
		simple("api.exemplo.com", "127.0.0.1:20002", false, ""),
		simple("exemplo.com", "127.0.0.1:20003", true, "www_to_apex"),
	}
}

// multiSite é o domínio com várias rotas, opções de endereço, cabeçalhos e
// compressão (golden validado com caddy validate).
func multiSite() Site {
	return Site{
		Host: "loja.exemplo.net", ForceHTTPS: true, WWWRedirect: "none",
		Routes: []SiteRoute{
			{Path: "/", Upstream: "127.0.0.1:20001"},
			{Path: "/api/", Upstream: "127.0.0.1:20002", MaxBodyBytes: 1048576},
			{Path: "/api/v2/", Upstream: "127.0.0.1:20003", NoBuffering: true},
		},
		Headers: []Header{
			{Name: "Strict-Transport-Security", Value: "max-age=31536000"},
			{Name: "X-Content-Type-Options", Value: "nosniff"},
			{Name: "Referrer-Policy", Value: "strict-origin-when-cross-origin"},
			{Name: "Permissions-Policy", Value: "camera=(), microphone=(), geolocation=()"},
		},
		Compression: true,
	}
}

func build(t *testing.T, sites []Site) []byte {
	t.Helper()
	got, err := BuildConfig(sites, ListenNormal, nil)
	require.NoError(t, err)
	return got
}

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(got))
	assert.True(t, SameConfig(want, got))
}

func TestBuildConfig_LegacySingleRouteGolden(t *testing.T) {
	assertGolden(t, "three-routes.json", build(t, threeSites()))
}

func TestBuildConfig_MultiRouteGolden(t *testing.T) {
	assertGolden(t, "multi-route-headers-compression.json", build(t, []Site{multiSite()}))
}

func TestBuildConfig_Empty(t *testing.T) {
	got := build(t, nil)
	require.True(t, json.Valid(got))

	var cfg map[string]any
	require.NoError(t, json.Unmarshal(got, &cfg))
	assert.Equal(t, "127.0.0.1:2019", cfg["admin"].(map[string]any)["listen"])

	apps := cfg["apps"].(map[string]any)
	assert.NotContains(t, apps, "tls", "sem hosts não há política de certificado")
	servers := apps["http"].(map[string]any)["servers"].(map[string]any)
	require.Len(t, servers, 2)
	for _, name := range []string{"korego_https", "korego_http"} {
		srv := servers[name].(map[string]any)
		routes := srv["routes"].([]any)
		require.Len(t, routes, 1, name)
		assertCatchAll(t, routes[0].(map[string]any), name)
	}
	assert.Equal(t, []any{":443"}, servers["korego_https"].(map[string]any)["listen"])
	assert.Equal(t, []any{":80"}, servers["korego_http"].(map[string]any)["listen"])
}

// assertCatchAll confere a rota final sem matcher que responde 404 a hosts
// não cadastrados (sem ela o Caddy devolve 200 vazio).
func assertCatchAll(t *testing.T, r map[string]any, name string) {
	t.Helper()
	assert.NotContains(t, r, "match", name)
	assert.Equal(t, true, r["terminal"], name)
	h := r["handle"].([]any)
	require.Len(t, h, 2, name)
	assert.Equal(t, strip, h[0], name)
	assert.Equal(t, map[string]any{
		"handler": "static_response", "status_code": float64(404),
		"body": "Domínio não configurado no KoreGO.",
	}, h[1], name)
}

func TestBuildConfig_CatchAllLast(t *testing.T) {
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(build(t, threeSites()), &cfg))
	servers := cfg["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	for _, name := range []string{"korego_https", "korego_http"} {
		routes := servers[name].(map[string]any)["routes"].([]any)
		require.Len(t, routes, 5, name)
		assertCatchAll(t, routes[4].(map[string]any), name)
		for _, r := range routes[:4] {
			assert.Contains(t, r.(map[string]any), "match", name)
		}
	}
}

func TestBuildConfig_Deterministic(t *testing.T) {
	s := append(threeSites(), multiSite())
	a := build(t, s)

	m := multiSite()
	m.Routes = []SiteRoute{m.Routes[2], m.Routes[0], m.Routes[1]}
	b := build(t, []Site{s[2], m, s[0], s[1]})
	assert.Equal(t, string(a), string(b))
	assert.Equal(t, string(a), string(build(t, s)))
	// a entrada não é reordenada no lugar
	assert.Equal(t, "app.exemplo.com", s[0].Host)
	assert.Equal(t, "/api/v2/", m.Routes[0].Path)
}

type handler = map[string]any

type parsedRoute struct {
	Match    []map[string][]string `json:"match"`
	Handle   []handler             `json:"handle"`
	Terminal bool                  `json:"terminal"`
}

type parsedConfig struct {
	Apps struct {
		HTTP struct {
			Servers map[string]struct {
				Routes []parsedRoute `json:"routes"`
				Errors struct {
					Routes []parsedRoute `json:"routes"`
				} `json:"errors"`
			} `json:"servers"`
		} `json:"http"`
		TLS struct {
			Automation struct {
				Policies []struct {
					Subjects []string `json:"subjects"`
				} `json:"policies"`
			} `json:"automation"`
		} `json:"tls"`
	} `json:"apps"`
}

func parse(t *testing.T, b []byte) parsedConfig {
	t.Helper()
	var cfg parsedConfig
	require.NoError(t, json.Unmarshal(b, &cfg))
	return cfg
}

func (c parsedConfig) routes(server string) []parsedRoute {
	return c.Apps.HTTP.Servers[server].Routes
}

func (c parsedConfig) errorRoutes(server string) []parsedRoute {
	return c.Apps.HTTP.Servers[server].Errors.Routes
}

// last devolve o último handler da rota de host (o que responde: subroute
// ou redirecionamento).
func last(r parsedRoute) handler { return r.Handle[len(r.Handle)-1] }

// subroutes devolve as rotas de caminho do subroute de uma rota de host.
func subroutes(t *testing.T, r parsedRoute) []parsedRoute {
	t.Helper()
	h := last(r)
	require.Equal(t, "subroute", h["handler"])
	b, err := json.Marshal(h["routes"])
	require.NoError(t, err)
	var out []parsedRoute
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

// pathOf devolve o matcher de caminho da rota ("" quando não há matcher).
func pathOf(r parsedRoute) string {
	if len(r.Match) == 0 {
		return ""
	}
	return r.Match[0]["path"][0]
}

// findSub acha a rota de caminho com o matcher p.
func findSub(t *testing.T, subs []parsedRoute, p string) parsedRoute {
	t.Helper()
	for _, r := range subs {
		if pathOf(r) == p {
			return r
		}
	}
	t.Fatalf("rota %q não encontrada", p)
	return parsedRoute{}
}

func location(h handler) any {
	return h["headers"].(map[string]any)["Location"]
}

func dial(h handler) any {
	return h["upstreams"].([]any)[0].(map[string]any)["dial"]
}

// strip é o handler que remove Server e Via (adiado).
var strip = handler{"handler": "headers", "response": map[string]any{
	"delete": []any{"Server", "Via"}, "deferred": true,
}}

func TestBuildConfig_PathRoutesLongestFirst(t *testing.T) {
	s := Site{Host: "exemplo.com", Routes: []SiteRoute{
		{Path: "/api/", Upstream: "127.0.0.1:2"},
		{Path: "/", Upstream: "127.0.0.1:1"},
		{Path: "/api/v2/", Upstream: "127.0.0.1:3"},
	}}
	cfg := parse(t, build(t, []Site{s}))
	for _, server := range []string{"korego_https", "korego_http"} {
		subs := subroutes(t, cfg.routes(server)[0])
		var paths, targets []any
		for _, r := range subs {
			assert.True(t, r.Terminal, server)
			paths = append(paths, pathOf(r))
			h := last(r)
			if h["handler"] == "reverse_proxy" {
				targets = append(targets, dial(h))
			} else {
				targets = append(targets, location(h))
			}
		}
		assert.Equal(t, []any{"/api/v2", "/api/v2/*", "/api", "/api/*", ""}, paths, server)
		assert.Equal(t, []any{
			[]any{"/api/v2/{http.request.uri.prefixed_query}"}, "127.0.0.1:3",
			[]any{"/api/{http.request.uri.prefixed_query}"}, "127.0.0.1:2",
			"127.0.0.1:1",
		}, targets, server)
	}
}

// Como o "location /api/" do nginx: /api (sem a barra) vai para /api/,
// mantendo a query string.
func TestBuildConfig_ExactPrefixRedirect(t *testing.T) {
	s := Site{Host: "exemplo.com", Routes: []SiteRoute{
		{Path: "/", Upstream: "127.0.0.1:1"},
		{Path: "/api/", Upstream: "127.0.0.1:2"},
	}}
	cfg := parse(t, build(t, []Site{s}))
	for _, server := range []string{"korego_https", "korego_http"} {
		r := findSub(t, subroutes(t, cfg.routes(server)[0]), "/api")
		assert.True(t, r.Terminal)
		require.Len(t, r.Handle, 1)
		assert.Equal(t, handler{
			"handler":     "static_response",
			"status_code": float64(308),
			"headers":     map[string]any{"Location": []any{"/api/{http.request.uri.prefixed_query}"}},
		}, r.Handle[0], server)
	}
	// "/" sozinho não ganha redirecionamento
	plain := parse(t, build(t, []Site{simple("exemplo.com", "127.0.0.1:1", false, "")}))
	assert.Len(t, subroutes(t, plain.routes("korego_https")[0]), 1)
}

// A rota "/" vai sem matcher (como na v0.4.0), para que OPTIONS * e
// afins continuem chegando ao upstream.
func TestBuildConfig_RootRouteNoMatcher(t *testing.T) {
	for _, s := range []Site{simple("exemplo.com", "127.0.0.1:1", false, ""), multiSite()} {
		cfg := parse(t, build(t, []Site{s}))
		subs := subroutes(t, cfg.routes("korego_https")[0])
		root := subs[len(subs)-1]
		assert.Empty(t, root.Match, s.Host)
		assert.Equal(t, "reverse_proxy", last(root)["handler"], s.Host)
		for _, r := range subs[:len(subs)-1] {
			assert.NotEmpty(t, r.Match, s.Host)
		}
	}
}

func TestBuildConfig_AddressOptions(t *testing.T) {
	s := Site{Host: "exemplo.com", Routes: []SiteRoute{
		{Path: "/", Upstream: "127.0.0.1:1"},
		{Path: "/upload/", Upstream: "127.0.0.1:2", MaxBodyBytes: 1048576},
		{Path: "/sse/", Upstream: "127.0.0.1:3", NoBuffering: true},
	}}
	cfg := parse(t, build(t, []Site{s}))
	subs := subroutes(t, cfg.routes("korego_https")[0])

	// /upload/: request_body antes do reverse_proxy
	up := findSub(t, subs, "/upload/*").Handle
	require.Len(t, up, 2)
	assert.Equal(t, handler{"handler": "request_body", "max_size": float64(1048576)}, up[0])
	assert.Equal(t, "reverse_proxy", up[1]["handler"])
	assert.NotContains(t, up[1], "flush_interval")

	// /sse/: flush_interval -1, sem request_body
	sse := findSub(t, subs, "/sse/*").Handle
	require.Len(t, sse, 1)
	assert.Equal(t, "reverse_proxy", sse[0]["handler"])
	assert.Equal(t, float64(-1), sse[0]["flush_interval"])

	// "/": sem opções, só reverse_proxy com X-Real-IP
	root := findSub(t, subs, "").Handle
	require.Len(t, root, 1)
	assert.NotContains(t, root[0], "flush_interval")
	assert.Equal(t, map[string]any{"request": map[string]any{"set": map[string]any{
		"X-Real-IP": []any{"{http.request.remote.host}"},
	}}}, root[0]["headers"])
}

func TestBuildConfig_HeadersAndCompression(t *testing.T) {
	cfg := parse(t, build(t, []Site{multiSite()}))
	r := cfg.routes("korego_https")[0]
	require.Len(t, r.Handle, 3)
	assert.Equal(t, handler{
		"handler": "headers",
		"response": map[string]any{
			"set": map[string]any{
				"Strict-Transport-Security": []any{"max-age=31536000"},
				"X-Content-Type-Options":    []any{"nosniff"},
				"Referrer-Policy":           []any{"strict-origin-when-cross-origin"},
				"Permissions-Policy":        []any{"camera=(), microphone=(), geolocation=()"},
			},
			"delete":   []any{"Server", "Via"},
			"deferred": true,
		},
	}, r.Handle[0])
	assert.Equal(t, handler{
		"handler":   "encode",
		"encodings": map[string]any{"zstd": map[string]any{}, "gzip": map[string]any{}},
		"prefer":    []any{"zstd", "gzip"},
	}, r.Handle[1])
	assert.Equal(t, "subroute", r.Handle[2]["handler"])

	// Sem cabeçalhos nem compressão: só o strip e o subroute.
	plain := parse(t, build(t, []Site{simple("exemplo.com", "127.0.0.1:1", false, "")}))
	r = plain.routes("korego_https")[0]
	require.Len(t, r.Handle, 2)
	assert.Equal(t, strip, r.Handle[0])
	assert.Equal(t, "subroute", r.Handle[1]["handler"])
	// :80 sem ForceHTTPS serve o mesmo conteúdo
	assert.Equal(t, r.Handle, plain.routes("korego_http")[0].Handle)
}

// Valores de cabeçalho são literais: chaves viram \{ e \}, que o replacer
// do Caddy devolve como { e } (caddy/replacer.go, phEscape). Sem isso,
// {env.HOME} sairia expandido em toda resposta.
func TestBuildConfig_HeaderValuesEscaped(t *testing.T) {
	s := simple("exemplo.com", "127.0.0.1:1", false, "")
	s.Headers = []Header{
		{Name: "X-Teste", Value: "{env.HOME}"},
		{Name: "Content-Security-Policy", Value: "default-src 'self'; script-src 'nonce-{x}' {http.request.host}"},
		{Name: "X-Barra", Value: `a\{b}\`},
	}
	cfg := parse(t, build(t, []Site{s}))
	set := cfg.routes("korego_https")[0].Handle[0]["response"].(map[string]any)["set"].(map[string]any)
	assert.Equal(t, []any{`\{env.HOME\}`}, set["X-Teste"])
	assert.Equal(t, []any{`default-src 'self'; script-src 'nonce-\{x\}' \{http.request.host\}`}, set["Content-Security-Policy"])
	assert.Equal(t, []any{`a\\{b\}\`}, set["X-Barra"])
	// os mesmos valores escapados nas rotas de erro
	errSet := cfg.errorRoutes("korego_https")[0].Handle[0]["response"].(map[string]any)["set"]
	assert.Equal(t, set, errSet)
}

// Erros gerados pelo Caddy (502 sem upstream, 413 do request_body) passam
// pelas rotas de erro: por host, com os cabeçalhos do domínio; no fim, uma
// rota sem matcher que só remove Server/Via.
func TestBuildConfig_ErrorRoutes(t *testing.T) {
	withHeaders := simple("exemplo.com", "127.0.0.1:1", false, "www_to_apex")
	withHeaders.Headers = []Header{{Name: "X-Content-Type-Options", Value: "nosniff"}}
	forced := simple("app.exemplo.com", "127.0.0.1:2", true, "")
	forced.Headers = []Header{{Name: "X-Frame-Options", Value: "DENY"}}
	bare := simple("api.exemplo.com", "127.0.0.1:3", false, "")
	cfg := parse(t, build(t, []Site{withHeaders, forced, bare}))

	errResp := handler{"handler": "static_response", "status_code": "{http.error.status_code}"}
	hdr := func(name, value string) handler {
		return handler{"handler": "headers", "response": map[string]any{
			"set":    map[string]any{name: []any{value}},
			"delete": []any{"Server", "Via"}, "deferred": true,
		}}
	}
	final := parsedRoute{Handle: []handler{strip, errResp}, Terminal: true}

	// :443: um por host com cabeçalhos, em ordem de host, e o final
	assert.Equal(t, []parsedRoute{
		{Match: []map[string][]string{{"host": {"app.exemplo.com"}}}, Handle: []handler{hdr("X-Frame-Options", "DENY"), errResp}, Terminal: true},
		{Match: []map[string][]string{{"host": {"exemplo.com"}}}, Handle: []handler{hdr("X-Content-Type-Options", "nosniff"), errResp}, Terminal: true},
		final,
	}, cfg.errorRoutes("korego_https"))
	// :80: app.exemplo.com só redireciona, então fica de fora
	assert.Equal(t, []parsedRoute{
		{Match: []map[string][]string{{"host": {"exemplo.com"}}}, Handle: []handler{hdr("X-Content-Type-Options", "nosniff"), errResp}, Terminal: true},
		final,
	}, cfg.errorRoutes("korego_http"))

	// sem sites, só a rota final
	empty := parse(t, build(t, nil))
	for _, server := range []string{"korego_https", "korego_http"} {
		assert.Equal(t, []parsedRoute{final}, empty.errorRoutes(server))
	}
}

// Toda rota remove Server e Via, inclusive os redirecionamentos e o 404
// final.
func TestBuildConfig_ServerRemovedOnEveryHostRoute(t *testing.T) {
	cfg := parse(t, build(t, threeSites()))
	for _, server := range []string{"korego_https", "korego_http"} {
		for _, r := range append(cfg.routes(server), cfg.errorRoutes(server)...) {
			h := r.Handle[0]
			assert.Equal(t, "headers", h["handler"], server)
			resp := h["response"].(map[string]any)
			assert.Equal(t, []any{"Server", "Via"}, resp["delete"], server)
			assert.Equal(t, true, resp["deferred"], server)
		}
	}
}

func TestBuildConfig_WWWRedirectStillWorks(t *testing.T) {
	// www_to_apex com ForceHTTPS
	cfg := parse(t, build(t, []Site{simple("exemplo.com", "127.0.0.1:20001", true, "www_to_apex")}))
	https := cfg.routes("korego_https")
	require.Len(t, https, 3)
	assert.Equal(t, []string{"exemplo.com"}, https[0].Match[0]["host"])
	assert.Equal(t, "subroute", last(https[0])["handler"])
	assert.Equal(t, []string{"www.exemplo.com"}, https[1].Match[0]["host"])
	assert.Equal(t, float64(308), last(https[1])["status_code"])
	assert.Equal(t, []any{"https://exemplo.com{http.request.uri}"}, location(last(https[1])))
	plain := cfg.routes("korego_http")
	assert.Equal(t, []any{"https://{http.request.host}{http.request.uri}"}, location(last(plain[0])))
	assert.Equal(t, []any{"https://exemplo.com{http.request.uri}"}, location(last(plain[1])))

	// apex_to_www sem ForceHTTPS
	cfg = parse(t, build(t, []Site{simple("www.exemplo.com", "127.0.0.1:20001", false, "apex_to_www")}))
	https = cfg.routes("korego_https")
	require.Len(t, https, 3)
	assert.Equal(t, []string{"exemplo.com"}, https[0].Match[0]["host"])
	assert.Equal(t, []any{"https://www.exemplo.com{http.request.uri}"}, location(last(https[0])))
	assert.Equal(t, []string{"www.exemplo.com"}, https[1].Match[0]["host"])
	assert.Equal(t, "subroute", last(https[1])["handler"])

	// sem ForceHTTPS, o host secundário na porta 80 redireciona para o principal em http
	plain = cfg.routes("korego_http")
	require.Len(t, plain, 3)
	assert.Equal(t, []any{"http://www.exemplo.com{http.request.uri}"}, location(last(plain[0])))
	assert.Equal(t, "subroute", last(plain[1])["handler"])

	require.Len(t, cfg.Apps.TLS.Automation.Policies, 1)
	assert.Equal(t, []string{"exemplo.com", "www.exemplo.com"}, cfg.Apps.TLS.Automation.Policies[0].Subjects)
}

func TestBuildConfig_NormalizesHost(t *testing.T) {
	cfg := parse(t, build(t, []Site{simple(" App.Exemplo.COM. ", "127.0.0.1:20001", false, "")}))
	assert.Equal(t, []string{"app.exemplo.com"}, cfg.routes("korego_https")[0].Match[0]["host"])
}

func TestBuildConfig_Invalid(t *testing.T) {
	ok := []SiteRoute{{Path: "/", Upstream: "127.0.0.1:1"}}
	cases := map[string][]Site{
		"host vazio":          {{Host: "", Routes: ok}},
		"host com porta":      {{Host: "a.exemplo.com:8080", Routes: ok}},
		"sem rotas":           {{Host: "a.exemplo.com"}},
		"upstream vazio":      {{Host: "a.exemplo.com", Routes: []SiteRoute{{Path: "/"}}}},
		"sem rota raiz":       {{Host: "a.exemplo.com", Routes: []SiteRoute{{Path: "/api/", Upstream: "127.0.0.1:1"}}}},
		"caminho sem barra":   {{Host: "a.exemplo.com", Routes: append(ok, SiteRoute{Path: "/api", Upstream: "127.0.0.1:1"})}},
		"caminho inválido":    {{Host: "a.exemplo.com", Routes: append(ok, SiteRoute{Path: "/a*b/", Upstream: "127.0.0.1:1"})}},
		"caminho vazio meio":  {{Host: "a.exemplo.com", Routes: append(ok, SiteRoute{Path: "/a//", Upstream: "127.0.0.1:1"})}},
		"caminho repetido":    {{Host: "a.exemplo.com", Routes: append(ok, SiteRoute{Path: "/", Upstream: "127.0.0.1:2"})}},
		"limite negativo":     {{Host: "a.exemplo.com", Routes: []SiteRoute{{Path: "/", Upstream: "127.0.0.1:1", MaxBodyBytes: -1}}}},
		"cabeçalho inválido":  {{Host: "a.exemplo.com", Routes: ok, Headers: []Header{{Name: "X Y", Value: "1"}}}},
		"cabeçalho com CRLF":  {{Host: "a.exemplo.com", Routes: ok, Headers: []Header{{Name: "X-Y", Value: "1\r\nZ: 2"}}}},
		"cabeçalho repetido":  {{Host: "a.exemplo.com", Routes: ok, Headers: []Header{{Name: "X-Y", Value: "1"}, {Name: "x-y", Value: "2"}}}},
		"www desconhecido":    {{Host: "a.exemplo.com", Routes: ok, WWWRedirect: "outro"}},
		"www_to_apex com www": {{Host: "www.exemplo.com", Routes: ok, WWWRedirect: "www_to_apex"}},
		"apex_to_www sem www": {{Host: "exemplo.com", Routes: ok, WWWRedirect: "apex_to_www"}},
		"tls desconhecido":    {{Host: "a.exemplo.com", Routes: ok, TLS: SiteTLS{Mode: "outro"}}},
		"host duplicado": {
			{Host: "a.exemplo.com", Routes: ok},
			{Host: "A.exemplo.com", Routes: ok},
		},
		"secundário colide": {
			{Host: "exemplo.com", Routes: ok, WWWRedirect: "www_to_apex"},
			{Host: "www.exemplo.com", Routes: ok},
		},
	}
	many := make([]Header, 31)
	for i := range many {
		many[i] = Header{Name: "X-H" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Value: "1"}
	}
	cases["cabeçalhos demais"] = []Site{{Host: "a.exemplo.com", Routes: ok, Headers: many}}
	routes := make([]SiteRoute, 21)
	routes[0] = ok[0]
	for i := 1; i < len(routes); i++ {
		routes[i] = SiteRoute{Path: "/r" + string(rune('a'+i)) + "/", Upstream: "127.0.0.1:1"}
	}
	cases["rotas demais"] = []Site{{Host: "a.exemplo.com", Routes: routes}}

	for name, sites := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := BuildConfig(sites, ListenNormal, nil)
			assert.Error(t, err)
		})
	}
}

// Modo de escuta desconhecido é erro; TLS "auto" explícito é o mesmo que
// vazio.
func TestBuildConfig_ModeAndAuto(t *testing.T) {
	base := simple("exemplo.com", "127.0.0.1:1", false, "")
	_, err := BuildConfig([]Site{base}, Listen("outro"), nil)
	assert.Error(t, err)

	auto := base
	auto.TLS = SiteTLS{Mode: TLSAuto}
	assert.Equal(t, string(build(t, []Site{base})), string(build(t, []Site{auto})))
	// "" é o mesmo que normal
	empty, err := BuildConfig([]Site{base}, "", nil)
	require.NoError(t, err)
	assert.Equal(t, string(build(t, []Site{base})), string(empty))
}

func TestSameConfig(t *testing.T) {
	cfg := build(t, threeSites())

	assert.True(t, SameConfig([]byte(`{"a": 1, "b": [1, 2]}`), []byte("{\n  \"b\":[1,2],\"a\":1}\n")))
	assert.False(t, SameConfig([]byte(`{"a":1}`), []byte(`{"a":2}`)))

	var generic map[string]any
	require.NoError(t, json.Unmarshal(cfg, &generic))
	indented, err := json.MarshalIndent(generic, "", "    ")
	require.NoError(t, err)
	assert.True(t, SameConfig(cfg, indented))

	assert.False(t, SameConfig([]byte("null"), cfg))
	assert.False(t, SameConfig(cfg, []byte("null\n")))
	assert.False(t, SameConfig(nil, cfg))
	assert.False(t, SameConfig([]byte("  "), cfg))
	assert.False(t, SameConfig([]byte("{inválido"), cfg))
	assert.True(t, SameConfig([]byte("null"), nil))
}

// --- Task 3: estático, certificado próprio, só Cloudflare e modo ensaio ---

// spaSite é o app.exemplo.com de referência: SPA em /var/www/app/current,
// cache imutável em /assets/, CSP e compressão.
func spaSite() Site {
	return Site{
		Host: "app.exemplo.com", ForceHTTPS: true,
		Routes: []SiteRoute{{Path: "/", Static: &StaticTarget{
			Root: "/var/www/app/current", SPA: true, ImmutablePrefix: "/assets/",
		}}},
		Headers: []Header{{Name: "Content-Security-Policy",
			Value: "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'"}},
		Compression: true,
	}
}

const (
	noCache   = "no-cache"
	immutable = "public, max-age=31536000, immutable"
)

func cacheHeaders(v string) handler {
	return handler{"handler": "headers", "response": map[string]any{
		"set": map[string]any{"Cache-Control": []any{v}}, "deferred": true,
	}}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestBuildConfig_StaticSPAGolden(t *testing.T) {
	got := build(t, []Site{spaSite()})
	dump(t, "static-spa.json", got)
	assertGolden(t, "static-spa.json", got)

	cfg := parse(t, got)
	// :80 só redireciona (ForceHTTPS); :443 serve a pasta
	r := cfg.routes("korego_https")[0]
	require.Len(t, r.Handle, 3)
	assert.Equal(t, []any{"default-src 'self'; img-src 'self' data:; frame-ancestors 'none'"},
		r.Handle[0]["response"].(map[string]any)["set"].(map[string]any)["Content-Security-Policy"])
	subs := subroutes(t, r)
	require.Len(t, subs, 1)
	root := subs[0]
	assert.Empty(t, root.Match)
	require.Len(t, root.Handle, 1)

	want := `{"handler":"subroute","routes":[
		{"match":[{"path":["/assets/*"]}],
		 "handle":[` + mustJSON(t, cacheHeaders(immutable)) + `,{"handler":"file_server","root":"/var/www/app/current","hide":[".git",".env*"]}],
		 "terminal":true},
		{"match":[{"file":{"root":"/var/www/app/current","try_files":["{http.request.uri.path}","/index.html"]}}],
		 "handle":[{"handler":"rewrite","uri":"{http.matchers.file.relative}"}],
		 "terminal":false},
		{"match":[{"path":["/","/index.html"]}],
		 "handle":[` + mustJSON(t, cacheHeaders(noCache)) + `],
		 "terminal":false},
		{"handle":[{"handler":"file_server","root":"/var/www/app/current","hide":[".git",".env*"]}],"terminal":true}
	]}`
	assert.JSONEq(t, want, mustJSON(t, root.Handle[0]))
}

// Sem spa nem immutablePrefix, a rota estática é só o file_server; o root
// sai limpo e com as chaves escapadas.
func TestBuildConfig_StaticPlain(t *testing.T) {
	s := Site{Host: "exemplo.com", Routes: []SiteRoute{
		{Path: "/", Upstream: "127.0.0.1:1"},
		{Path: "/docs/", Static: &StaticTarget{Root: "/var/www/docs/./{x}/"}},
	}}
	cfg := parse(t, build(t, []Site{s}))
	subs := subroutes(t, cfg.routes("korego_https")[0])
	docs := findSub(t, subs, "/docs/*")
	require.Len(t, docs.Handle, 1)
	assert.JSONEq(t, `{"handler":"subroute","routes":[
		{"handle":[{"handler":"rewrite","strip_path_prefix":"/docs"}],"terminal":false},
		{"handle":[{"handler":"file_server","root":"/var/www/docs/\\{x\\}","hide":[".git",".env*"]}],"terminal":true}
	]}`, mustJSON(t, docs.Handle[0]))
	// o /docs → /docs/ continua
	findSub(t, subs, "/docs")
}

// selfSigned gera um certificado autoassinado e a chave, em PEM, para hosts.
func selfSigned(t *testing.T, hosts ...string) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: hosts[0]},
		DNSNames:     hosts,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	require.NoError(t, err)
	kder, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kder}))
}

type tlsParsed struct {
	Apps struct {
		HTTP struct {
			Servers map[string]struct {
				AutomaticHTTPS struct {
					SkipCertificates []string `json:"skip_certificates"`
				} `json:"automatic_https"`
			} `json:"servers"`
		} `json:"http"`
		TLS struct {
			Certificates struct {
				LoadPEM []struct {
					Certificate string `json:"certificate"`
					Key         string `json:"key"`
				} `json:"load_pem"`
			} `json:"certificates"`
			Automation *struct {
				Policies []struct {
					Subjects []string `json:"subjects"`
				} `json:"policies"`
			} `json:"automation"`
		} `json:"tls"`
	} `json:"apps"`
}

func TestBuildConfig_CustomTLS(t *testing.T) {
	cert, key := selfSigned(t, "exemplo.com", "www.exemplo.com", "loja.exemplo.net")
	// chave antes da cadeia, com texto solto: a divisão é por bloco PEM
	bundle := "comentário\n" + key + cert

	a := simple("exemplo.com", "127.0.0.1:1", true, "www_to_apex")
	a.TLS = SiteTLS{Mode: TLSCustom, CertPEM: bundle}
	b := simple("loja.exemplo.net", "127.0.0.1:2", false, "")
	b.TLS = SiteTLS{Mode: TLSCustom, CertPEM: bundle}
	auto := simple("app.exemplo.com", "127.0.0.1:3", false, "")

	got := build(t, []Site{a, b, auto})
	dump(t, "custom-tls.json", got)
	var cfg tlsParsed
	require.NoError(t, json.Unmarshal(got, &cfg))

	// um único load_pem para os dois sites
	require.Len(t, cfg.Apps.TLS.Certificates.LoadPEM, 1)
	assert.Equal(t, cert, cfg.Apps.TLS.Certificates.LoadPEM[0].Certificate)
	assert.Equal(t, key, cfg.Apps.TLS.Certificates.LoadPEM[0].Key)

	// só o host auto tem política de automação
	require.NotNil(t, cfg.Apps.TLS.Automation)
	require.Len(t, cfg.Apps.TLS.Automation.Policies, 1)
	assert.Equal(t, []string{"app.exemplo.com"}, cfg.Apps.TLS.Automation.Policies[0].Subjects)
	// e os próprios ficam fora do HTTPS automático (sem ACME)
	assert.Equal(t, []string{"exemplo.com", "loja.exemplo.net", "www.exemplo.com"},
		cfg.Apps.HTTP.Servers["korego_https"].AutomaticHTTPS.SkipCertificates)

	// dois PEMs diferentes, duas entradas; só próprios, sem automação
	cert2, key2 := selfSigned(t, "loja.exemplo.net")
	b.TLS.CertPEM = cert2 + key2
	var two tlsParsed
	require.NoError(t, json.Unmarshal(build(t, []Site{a, b}), &two))
	assert.Len(t, two.Apps.TLS.Certificates.LoadPEM, 2)
	assert.Nil(t, two.Apps.TLS.Automation)
}

var cfRanges = []string{"173.245.48.0/20", "103.21.244.0/22", "2400:cb00::/32"}

type cfParsed struct {
	Apps struct {
		HTTP struct {
			HTTPPort  int `json:"http_port"`
			HTTPSPort int `json:"https_port"`
			Servers   map[string]struct {
				Listen         []string `json:"listen"`
				TrustedProxies *struct {
					Source string   `json:"source"`
					Ranges []string `json:"ranges"`
				} `json:"trusted_proxies"`
				ClientIPHeaders []string `json:"client_ip_headers"`
			} `json:"servers"`
		} `json:"http"`
	} `json:"apps"`
}

type parsedRouteAny struct {
	Match    []map[string]any `json:"match"`
	Handle   []handler        `json:"handle"`
	Terminal bool             `json:"terminal"`
}

func wantDeny(host string, allow []string) parsedRouteAny {
	ranges := make([]any, len(allow))
	for i, r := range allow {
		ranges[i] = r
	}
	return parsedRouteAny{
		Match: []map[string]any{{
			"host": []any{host},
			"not":  []any{map[string]any{"remote_ip": map[string]any{"ranges": ranges}}},
		}},
		Handle:   []handler{strip, {"handler": "static_response", "status_code": float64(403)}},
		Terminal: true,
	}
}

func routesAny(t *testing.T, b []byte, server string) []parsedRouteAny {
	t.Helper()
	var cfg struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Routes []parsedRouteAny `json:"routes"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	require.NoError(t, json.Unmarshal(b, &cfg))
	return cfg.Apps.HTTP.Servers[server].Routes
}

func hostOf(r parsedRouteAny) any {
	if len(r.Match) == 0 {
		return nil
	}
	return r.Match[0]["host"].([]any)[0]
}

func TestBuildConfig_CloudflareOnly(t *testing.T) {
	cfSite := Site{Host: "exemplo.com", WWWRedirect: WWWToApex, ForceHTTPS: true, CloudflareOnly: true,
		Routes: []SiteRoute{
			{Path: "/", Upstream: "127.0.0.1:8091"},
			{Path: "/api/", Upstream: "127.0.0.1:8092"},
		}}
	other := simple("api.exemplo.com", "127.0.0.1:3", false, "")
	got, err := BuildConfig([]Site{cfSite, other}, ListenNormal, cfRanges)
	require.NoError(t, err)
	dump(t, "cloudflare-only.json", got)

	for _, server := range []string{"korego_https", "korego_http"} {
		routes := routesAny(t, got, server)
		// api (sem CF), exemplo.com (deny + rota), www (deny + rota), catch-all
		require.Len(t, routes, 6, server)
		assert.Equal(t, "api.exemplo.com", hostOf(routes[0]), server)
		assert.Equal(t, wantDeny("exemplo.com", cfRanges), routes[1], server)
		assert.Equal(t, "exemplo.com", hostOf(routes[2]), server)
		assert.NotContains(t, routes[2].Match[0], "not", server)
		assert.Equal(t, wantDeny("www.exemplo.com", cfRanges), routes[3], server)
		assert.Equal(t, "www.exemplo.com", hostOf(routes[4]), server)
	}

	var cfg cfParsed
	require.NoError(t, json.Unmarshal(got, &cfg))
	for _, server := range []string{"korego_https", "korego_http"} {
		srv := cfg.Apps.HTTP.Servers[server]
		require.NotNil(t, srv.TrustedProxies, server)
		assert.Equal(t, "static", srv.TrustedProxies.Source, server)
		assert.Equal(t, cfRanges, srv.TrustedProxies.Ranges, server)
		assert.Equal(t, []string{"CF-Connecting-IP"}, srv.ClientIPHeaders, server)
	}

	// X-Real-IP: client_ip no site CF, endereço do par no outro
	https := routesAny(t, got, "korego_https")
	realIP := func(r parsedRoute) any {
		return last(r)["headers"].(map[string]any)["request"].(map[string]any)["set"].(map[string]any)["X-Real-IP"]
	}
	xff := func(r parsedRoute) any {
		return last(r)["headers"].(map[string]any)["request"].(map[string]any)["set"].(map[string]any)["X-Forwarded-For"]
	}
	n := 0
	for _, sub := range subroutes(t, parsedRoute{Handle: https[2].Handle}) {
		if last(sub)["handler"] == "reverse_proxy" {
			assert.Equal(t, []any{"{http.vars.client_ip}"}, realIP(sub))
			// O upstream (o painel, uma app) vê o visitante, não a Cloudflare.
			assert.Equal(t, []any{"{http.vars.client_ip}"}, xff(sub))
			n++
		}
	}
	assert.Equal(t, 2, n)
	assert.Equal(t, []any{"{http.request.remote.host}"}, realIP(subroutes(t, parsedRoute{Handle: https[0].Handle})[0]))
	assert.Nil(t, xff(subroutes(t, parsedRoute{Handle: https[0].Handle})[0]), "fora do só Cloudflare, o X-Forwarded-For padrão do Caddy")
	assertGolden(t, "cloudflare-only.json", got)

	// sem site CF, as faixas não mudam nada
	plain, err := BuildConfig([]Site{other}, ListenNormal, cfRanges)
	require.NoError(t, err)
	assert.Equal(t, string(build(t, []Site{other})), string(plain))
	// e nem são validadas: uma faixa ruim no cache não derruba quem não usa CF
	bad, err := BuildConfig([]Site{other}, ListenNormal, []string{"lixo", "203.0.113.0/33"})
	require.NoError(t, err)
	assert.Equal(t, string(plain), string(bad))
}

func TestBuildConfig_TrialListen(t *testing.T) {
	cfSite := simple("exemplo.com", "127.0.0.1:1", false, "")
	cfSite.CloudflareOnly = true
	auto := simple("app.exemplo.com", "127.0.0.1:2", false, "")
	got, err := BuildConfig([]Site{cfSite, auto}, ListenTrial, cfRanges)
	require.NoError(t, err)
	dump(t, "trial.json", got)

	var cfg cfParsed
	require.NoError(t, json.Unmarshal(got, &cfg))
	assert.Equal(t, []string{"127.0.0.1:18443"}, cfg.Apps.HTTP.Servers["korego_https"].Listen)
	assert.Equal(t, []string{"127.0.0.1:18080"}, cfg.Apps.HTTP.Servers["korego_http"].Listen)
	// as portas do app http acompanham, para o Caddy saber qual servidor é o HTTP
	assert.Equal(t, 18080, cfg.Apps.HTTP.HTTPPort)
	assert.Equal(t, 18443, cfg.Apps.HTTP.HTTPSPort)

	allow := append(append([]string(nil), cfRanges...), "127.0.0.1/32", "::1/128")
	for _, server := range []string{"korego_https", "korego_http"} {
		routes := routesAny(t, got, server)
		assert.Equal(t, "app.exemplo.com", hostOf(routes[0]), server)
		assert.Equal(t, wantDeny("exemplo.com", allow), routes[1], server)
	}

	var p tlsParsed
	require.NoError(t, json.Unmarshal(got, &p))
	require.NotNil(t, p.Apps.TLS.Automation)
	require.Len(t, p.Apps.TLS.Automation.Policies, 1)
	assert.Equal(t, []string{"app.exemplo.com", "exemplo.com"}, p.Apps.TLS.Automation.Policies[0].Subjects)
	// no ensaio, emissor interno: nada de Let's Encrypt enquanto outro dono tem a :80
	assert.Equal(t, []any{map[string]any{"module": "internal"}}, policyIssuers(t, got))

	// modo normal não define portas nem emissor
	normalCfg := build(t, []Site{auto})
	var normal cfParsed
	require.NoError(t, json.Unmarshal(normalCfg, &normal))
	assert.Zero(t, normal.Apps.HTTP.HTTPPort)
	assert.Zero(t, normal.Apps.HTTP.HTTPSPort)
	assert.Nil(t, policyIssuers(t, normalCfg))
}

// policyIssuers devolve o campo issuers da única política de automação.
func policyIssuers(t *testing.T, b []byte) any {
	t.Helper()
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(b, &cfg))
	tls := cfg["apps"].(map[string]any)["tls"].(map[string]any)
	pols := tls["automation"].(map[string]any)["policies"].([]any)
	require.Len(t, pols, 1)
	return pols[0].(map[string]any)["issuers"]
}

func TestBuildConfig_RejectsBadInput(t *testing.T) {
	cert, key := selfSigned(t, "exemplo.com")
	other, _ := selfSigned(t, "exemplo.com")
	st := func(root string) Site {
		return Site{Host: "exemplo.com", Routes: []SiteRoute{{Path: "/", Static: &StaticTarget{Root: root}}}}
	}
	tlsSite := func(p string) Site {
		s := simple("exemplo.com", "127.0.0.1:1", false, "")
		s.TLS = SiteTLS{Mode: TLSCustom, CertPEM: p}
		return s
	}
	cfSite := simple("exemplo.com", "127.0.0.1:1", false, "")
	cfSite.CloudflareOnly = true
	prefix := func(p string) Site {
		s := st("/var/www/app")
		s.Routes[0].Static.ImmutablePrefix = p
		return s
	}
	both := Site{Host: "exemplo.com", Routes: []SiteRoute{{Path: "/", Upstream: "127.0.0.1:1",
		Static: &StaticTarget{Root: "/var/www/a"}}}}

	cases := map[string]struct {
		sites []Site
		cf    []string
	}{
		"root fora de /var/www":  {sites: []Site{st("/etc")}},
		"root com ..":            {sites: []Site{st("/var/www/../etc")}},
		"root vizinho":           {sites: []Site{st("/var/www-evil")}},
		"root relativo":          {sites: []Site{st("var/www/app")}},
		"root vazio":             {sites: []Site{st("")}},
		"estático e upstream":    {sites: []Site{both}},
		"prefixo inválido":       {sites: []Site{prefix("assets")}},
		"prefixo raiz":           {sites: []Site{prefix("/")}},
		"pem sem chave":          {sites: []Site{tlsSite(cert)}},
		"pem sem certificado":    {sites: []Site{tlsSite(key)}},
		"pem vazio":              {sites: []Site{tlsSite("")}},
		"pem com duas chaves":    {sites: []Site{tlsSite(cert + key + key)}},
		"chave não confere":      {sites: []Site{tlsSite(other + key)}},
		"cf vazio com só CF":     {sites: []Site{cfSite}},
		"faixa cf inválida":      {sites: []Site{cfSite}, cf: []string{"203.0.113.0/33"}},
		"host com chave":         {sites: []Site{simple("{env.X}.exemplo.com", "127.0.0.1:1", false, "")}},
		"upstream com chave":     {sites: []Site{simple("exemplo.com", "{env.UP}", false, "")}},
		"upstream com chave fim": {sites: []Site{simple("exemplo.com", "127.0.0.1:1}", false, "")}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := BuildConfig(c.sites, ListenNormal, c.cf)
			assert.Error(t, err)
		})
	}

	// as variantes válidas passam
	for name, c := range map[string]struct {
		sites []Site
		cf    []string
	}{
		"root /var/www":  {sites: []Site{st("/var/www")}},
		"prefixo válido": {sites: []Site{prefix("/assets/")}},
		"pem completo":   {sites: []Site{tlsSite(cert + key)}},
		"cf com faixas":  {sites: []Site{cfSite}, cf: []string{"203.0.113.0/24"}},
	} {
		_, err := BuildConfig(c.sites, ListenNormal, c.cf)
		assert.NoError(t, err, name)
	}
}

// dump grava a config em $KOREGO_CADDY_DUMP/<name>, para validar num Caddy
// real (caddy validate e checagens em execução). Sem a variável, não faz nada.
func dump(t *testing.T, name string, b []byte) {
	t.Helper()
	dir := os.Getenv("KOREGO_CADDY_DUMP")
	if dir == "" {
		return
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), b, 0o644))
}

// Rota estática fora da raiz: semântica de alias (como handle_path). /app/x
// vem de R/x, o fallback é R/index.html e immutablePrefix é relativo à rota.
func TestBuildConfig_StaticPrefixGolden(t *testing.T) {
	s := Site{Host: "exemplo.com", Routes: []SiteRoute{
		{Path: "/", Upstream: "127.0.0.1:8091"},
		{Path: "/app/", Static: &StaticTarget{Root: "/var/www/app/current", SPA: true, ImmutablePrefix: "/assets/"}},
	}}
	got := build(t, []Site{s})
	dump(t, "static-prefix.json", got)
	assertGolden(t, "static-prefix.json", got)

	cfg := parse(t, got)
	subs := subroutes(t, cfg.routes("korego_https")[0])
	// /app → /app/ continua
	assert.Equal(t, []any{"/app/{http.request.uri.prefixed_query}"}, location(last(findSub(t, subs, "/app"))))
	app := findSub(t, subs, "/app/*")
	require.Len(t, app.Handle, 1)
	want := `{"handler":"subroute","routes":[
		{"handle":[{"handler":"rewrite","strip_path_prefix":"/app"}],"terminal":false},
		{"match":[{"path":["/assets/*"]}],
		 "handle":[` + mustJSON(t, cacheHeaders(immutable)) + `,{"handler":"file_server","root":"/var/www/app/current","hide":[".git",".env*"]}],
		 "terminal":true},
		{"match":[{"file":{"root":"/var/www/app/current","try_files":["{http.request.uri.path}","/index.html"]}}],
		 "handle":[{"handler":"rewrite","uri":"{http.matchers.file.relative}"}],
		 "terminal":false},
		{"match":[{"path":["/","/index.html"]}],
		 "handle":[` + mustJSON(t, cacheHeaders(noCache)) + `],
		 "terminal":false},
		{"handle":[{"handler":"file_server","root":"/var/www/app/current","hide":[".git",".env*"]}],"terminal":true}
	]}`
	assert.JSONEq(t, want, mustJSON(t, app.Handle[0]))
}

// A config carrega chaves privadas (load_pem): o Caddy não pode gravá-la em
// /config/caddy/autosave.json.
func TestBuildConfig_AdminDoesNotPersist(t *testing.T) {
	for _, mode := range []Listen{ListenNormal, ListenTrial} {
		got, err := BuildConfig([]Site{multiSite()}, mode, nil)
		require.NoError(t, err)
		var cfg struct {
			Admin struct {
				Listen string `json:"listen"`
				Config *struct {
					Persist *bool `json:"persist"`
				} `json:"config"`
			} `json:"admin"`
		}
		require.NoError(t, json.Unmarshal(got, &cfg))
		assert.Equal(t, AdminListen, cfg.Admin.Listen)
		require.NotNil(t, cfg.Admin.Config, mode)
		require.NotNil(t, cfg.Admin.Config.Persist, mode)
		assert.False(t, *cfg.Admin.Config.Persist, mode)
	}
}
