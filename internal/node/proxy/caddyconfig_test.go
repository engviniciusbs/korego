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
	assert.Equal(t, map[string]any{"handler": "headers", "response": map[string]any{
		"delete": []any{"Server"}, "deferred": true,
	}}, h[0], name)
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

// last devolve o último handler da rota de host (o que responde: subroute
// ou redirecionamento).
func last(r parsedRoute) handler { return r.Handle[len(r.Handle)-1] }

// subroutes devolve as rotas de caminho do subroute de uma rota de host.
func subroutes(t *testing.T, r parsedRoute) []any {
	t.Helper()
	h := last(r)
	require.Equal(t, "subroute", h["handler"])
	return h["routes"].([]any)
}

func location(h handler) any {
	return h["headers"].(map[string]any)["Location"]
}

func TestBuildConfig_PathRoutesLongestFirst(t *testing.T) {
	s := Site{Host: "exemplo.com", Routes: []SiteRoute{
		{Path: "/api/", Upstream: "127.0.0.1:2"},
		{Path: "/", Upstream: "127.0.0.1:1"},
		{Path: "/api/v2/", Upstream: "127.0.0.1:3"},
	}}
	cfg := parse(t, build(t, []Site{s}))
	for _, server := range []string{"korego_https", "korego_http"} {
		subs := subroutes(t, cfg.routes(server)[0])
		require.Len(t, subs, 3)
		var paths []any
		var dials []any
		for _, r := range subs {
			m := r.(map[string]any)
			assert.Equal(t, true, m["terminal"])
			paths = append(paths, m["match"].([]any)[0].(map[string]any)["path"].([]any)[0])
			h := m["handle"].([]any)
			rp := h[len(h)-1].(map[string]any)
			dials = append(dials, rp["upstreams"].([]any)[0].(map[string]any)["dial"])
		}
		assert.Equal(t, []any{"/api/v2/*", "/api/*", "/*"}, paths, server)
		assert.Equal(t, []any{"127.0.0.1:3", "127.0.0.1:2", "127.0.0.1:1"}, dials, server)
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
	require.Len(t, subs, 3)
	handles := func(i int) []any { return subs[i].(map[string]any)["handle"].([]any) }
	path := func(i int) any { return subs[i].(map[string]any)["match"].([]any)[0].(map[string]any)["path"] }
	require.Equal(t, []any{"/upload/*"}, path(0))
	require.Equal(t, []any{"/sse/*"}, path(1))
	require.Equal(t, []any{"/*"}, path(2))

	// /upload/: request_body antes do reverse_proxy
	up := handles(0)
	require.Len(t, up, 2)
	assert.Equal(t, map[string]any{"handler": "request_body", "max_size": float64(1048576)}, up[0])
	assert.Equal(t, "reverse_proxy", up[1].(map[string]any)["handler"])
	assert.NotContains(t, up[1].(map[string]any), "flush_interval")

	// /sse/: flush_interval -1, sem request_body
	sse := handles(1)
	require.Len(t, sse, 1)
	assert.Equal(t, "reverse_proxy", sse[0].(map[string]any)["handler"])
	assert.Equal(t, float64(-1), sse[0].(map[string]any)["flush_interval"])

	// "/": sem opções, só reverse_proxy com X-Real-IP
	root := handles(2)
	require.Len(t, root, 1)
	rp := root[0].(map[string]any)
	assert.NotContains(t, rp, "flush_interval")
	assert.Equal(t, map[string]any{"request": map[string]any{"set": map[string]any{
		"X-Real-IP": []any{"{http.request.remote.host}"},
	}}}, rp["headers"])
}

func TestBuildConfig_HeadersAndCompression(t *testing.T) {
	cfg := parse(t, build(t, []Site{multiSite()}))
	for _, server := range []string{"korego_https"} {
		r := cfg.routes(server)[0]
		require.Len(t, r.Handle, 3, server)
		assert.Equal(t, handler{
			"handler": "headers",
			"response": map[string]any{
				"set": map[string]any{
					"Strict-Transport-Security": []any{"max-age=31536000"},
					"X-Content-Type-Options":    []any{"nosniff"},
					"Referrer-Policy":           []any{"strict-origin-when-cross-origin"},
					"Permissions-Policy":        []any{"camera=(), microphone=(), geolocation=()"},
				},
				"delete":   []any{"Server"},
				"deferred": true,
			},
		}, r.Handle[0], server)
		assert.Equal(t, handler{
			"handler":   "encode",
			"encodings": map[string]any{"zstd": map[string]any{}, "gzip": map[string]any{}},
			"prefer":    []any{"zstd", "gzip"},
		}, r.Handle[1], server)
		assert.Equal(t, "subroute", r.Handle[2]["handler"], server)
	}

	// Sem cabeçalhos nem compressão: só o delete do Server e o subroute.
	plain := parse(t, build(t, []Site{simple("exemplo.com", "127.0.0.1:1", false, "")}))
	r := plain.routes("korego_https")[0]
	require.Len(t, r.Handle, 2)
	assert.Equal(t, handler{"handler": "headers", "response": map[string]any{
		"delete": []any{"Server"}, "deferred": true,
	}}, r.Handle[0])
	assert.Equal(t, "subroute", r.Handle[1]["handler"])
	// :80 sem ForceHTTPS serve o mesmo conteúdo
	assert.Equal(t, r.Handle, plain.routes("korego_http")[0].Handle)
}

// Toda rota remove o Server, inclusive os redirecionamentos e o 404 final.
func TestBuildConfig_ServerRemovedOnEveryHostRoute(t *testing.T) {
	cfg := parse(t, build(t, threeSites()))
	for _, server := range []string{"korego_https", "korego_http"} {
		routes := cfg.routes(server)
		for _, r := range routes {
			h := r.Handle[0]
			assert.Equal(t, "headers", h["handler"], server)
			resp := h["response"].(map[string]any)
			assert.Equal(t, []any{"Server"}, resp["delete"], server)
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
