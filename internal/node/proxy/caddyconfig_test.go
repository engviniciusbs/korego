package proxy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func threeRoutes() []Route {
	return []Route{
		{Host: "app.exemplo.com", Upstream: "127.0.0.1:20001", ForceHTTPS: true, WWWRedirect: "none"},
		{Host: "api.exemplo.com", Upstream: "127.0.0.1:20002", ForceHTTPS: false},
		{Host: "exemplo.com", Upstream: "127.0.0.1:20003", ForceHTTPS: true, WWWRedirect: "www_to_apex"},
	}
}

func TestBuildConfig_Golden(t *testing.T) {
	got, err := BuildConfig(threeRoutes())
	require.NoError(t, err)

	want, err := os.ReadFile(filepath.Join("testdata", "three-routes.json"))
	require.NoError(t, err)

	assert.JSONEq(t, string(want), string(got))
	assert.True(t, SameConfig(want, got))
}

func TestBuildConfig_Empty(t *testing.T) {
	got, err := BuildConfig(nil)
	require.NoError(t, err)
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
		assert.Empty(t, srv["routes"], name)
	}
	assert.Equal(t, []any{":443"}, servers["korego_https"].(map[string]any)["listen"])
	assert.Equal(t, []any{":80"}, servers["korego_http"].(map[string]any)["listen"])
}

func TestBuildConfig_Deterministic(t *testing.T) {
	r := threeRoutes()
	a, err := BuildConfig(r)
	require.NoError(t, err)
	b, err := BuildConfig([]Route{r[2], r[0], r[1]})
	require.NoError(t, err)
	assert.Equal(t, string(a), string(b))
	// a entrada não é reordenada no lugar
	assert.Equal(t, "app.exemplo.com", r[0].Host)
}

type parsedConfig struct {
	Apps struct {
		HTTP struct {
			Servers map[string]struct {
				Routes []struct {
					Match  []map[string][]string `json:"match"`
					Handle []map[string]any      `json:"handle"`
				} `json:"routes"`
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

func location(h map[string]any) any {
	return h["headers"].(map[string]any)["Location"]
}

func TestBuildConfig_ApexToWWW(t *testing.T) {
	got, err := BuildConfig([]Route{{Host: "www.exemplo.com", Upstream: "127.0.0.1:20001", WWWRedirect: "apex_to_www"}})
	require.NoError(t, err)

	var cfg parsedConfig
	require.NoError(t, json.Unmarshal(got, &cfg))

	https := cfg.Apps.HTTP.Servers["korego_https"].Routes
	require.Len(t, https, 2)
	assert.Equal(t, []string{"exemplo.com"}, https[0].Match[0]["host"])
	assert.Equal(t, "static_response", https[0].Handle[0]["handler"])
	assert.Equal(t, []any{"https://www.exemplo.com{http.request.uri}"}, location(https[0].Handle[0]))
	assert.Equal(t, []string{"www.exemplo.com"}, https[1].Match[0]["host"])
	assert.Equal(t, "reverse_proxy", https[1].Handle[0]["handler"])

	// sem ForceHTTPS, o host secundário na porta 80 redireciona para o principal em http
	plain := cfg.Apps.HTTP.Servers["korego_http"].Routes
	require.Len(t, plain, 2)
	assert.Equal(t, []any{"http://www.exemplo.com{http.request.uri}"}, location(plain[0].Handle[0]))
	assert.Equal(t, "reverse_proxy", plain[1].Handle[0]["handler"])

	require.Len(t, cfg.Apps.TLS.Automation.Policies, 1)
	assert.Equal(t, []string{"exemplo.com", "www.exemplo.com"}, cfg.Apps.TLS.Automation.Policies[0].Subjects)
}

func TestBuildConfig_NormalizesHost(t *testing.T) {
	got, err := BuildConfig([]Route{{Host: " App.Exemplo.COM. ", Upstream: "127.0.0.1:20001"}})
	require.NoError(t, err)
	var cfg parsedConfig
	require.NoError(t, json.Unmarshal(got, &cfg))
	assert.Equal(t, []string{"app.exemplo.com"}, cfg.Apps.HTTP.Servers["korego_https"].Routes[0].Match[0]["host"])
}

func TestBuildConfig_Invalid(t *testing.T) {
	cases := map[string][]Route{
		"host vazio":          {{Host: "", Upstream: "127.0.0.1:1"}},
		"host com porta":      {{Host: "a.exemplo.com:8080", Upstream: "127.0.0.1:1"}},
		"upstream vazio":      {{Host: "a.exemplo.com"}},
		"www desconhecido":    {{Host: "a.exemplo.com", Upstream: "127.0.0.1:1", WWWRedirect: "outro"}},
		"www_to_apex com www": {{Host: "www.exemplo.com", Upstream: "127.0.0.1:1", WWWRedirect: "www_to_apex"}},
		"apex_to_www sem www": {{Host: "exemplo.com", Upstream: "127.0.0.1:1", WWWRedirect: "apex_to_www"}},
		"host duplicado": {
			{Host: "a.exemplo.com", Upstream: "127.0.0.1:1"},
			{Host: "A.exemplo.com", Upstream: "127.0.0.1:2"},
		},
		"secundário colide": {
			{Host: "exemplo.com", Upstream: "127.0.0.1:1", WWWRedirect: "www_to_apex"},
			{Host: "www.exemplo.com", Upstream: "127.0.0.1:2"},
		},
	}
	for name, routes := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := BuildConfig(routes)
			assert.Error(t, err)
		})
	}
}

func TestSameConfig(t *testing.T) {
	cfg, err := BuildConfig(threeRoutes())
	require.NoError(t, err)

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
