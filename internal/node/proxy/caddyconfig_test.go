package proxy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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

// Recursos que chegam na Task 3: por enquanto, erro claro.
func TestBuildConfig_NotSupportedYet(t *testing.T) {
	base := simple("exemplo.com", "127.0.0.1:1", false, "")
	static := base
	static.Routes = []SiteRoute{{Path: "/", Static: &StaticTarget{Root: "/var/www/site"}}}
	custom := base
	custom.TLS = SiteTLS{Mode: TLSCustom, CertPEM: "x"}
	cf := base
	cf.CloudflareOnly = true

	for name, c := range map[string]struct {
		sites []Site
		mode  Listen
		cf    []string
	}{
		"static":         {[]Site{static}, ListenNormal, nil},
		"tls custom":     {[]Site{custom}, ListenNormal, nil},
		"cloudflareOnly": {[]Site{cf}, ListenNormal, nil},
		"faixas cf":      {[]Site{base}, ListenNormal, []string{"203.0.113.0/24"}},
		"trial":          {[]Site{base}, ListenTrial, nil},
		"modo inválido":  {[]Site{base}, Listen("outro"), nil},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := BuildConfig(c.sites, c.mode, c.cf)
			assert.Error(t, err)
		})
	}

	// TLS "auto" explícito é o mesmo que vazio
	auto := base
	auto.TLS = SiteTLS{Mode: TLSAuto}
	assert.Equal(t, string(build(t, []Site{base})), string(build(t, []Site{auto})))
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
