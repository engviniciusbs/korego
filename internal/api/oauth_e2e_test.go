package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
	kauth "github.com/engviniciusbs/korego/internal/auth"
)

// TestOAuthE2E_SDKClient conecta o cliente OAuth real do go-sdk (DCR + PKCE) ao
// /mcp, passando por descoberta, registro, consentimento com 2FA e troca do código.
func TestOAuthE2E_SDKClient(t *testing.T) {
	var origin string
	tk := &kauth.Tokens{Key: kauth.DeriveKey([]byte("chave-de-teste"), "oauth-refresh")}
	e := newEnvDeps(t, api.Deps{Tokens: tk,
		OAuthOrigin: func(context.Context) (string, bool) { return origin, origin != "" }})
	tk.DB, tk.Now = e.svc.DB, e.svc.Now
	secret, cookie, csrf := e.setup(t)

	srv := httptest.NewTLSServer(api.OAuthRouterForTest(e.deps, 0))
	defer srv.Close()
	origin = srv.URL

	// O "navegador": não segue redirecionamentos e confia no certificado do teste.
	browser := *srv.Client()
	browser.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	fetcher := func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, args.URL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := browser.Do(req)
		if err != nil {
			return nil, err
		}
		resp.Body.Close()
		loc, err := url.Parse(resp.Header.Get("Location"))
		if err != nil || resp.StatusCode != http.StatusFound {
			return nil, errors.New("authorize não redirecionou: " + resp.Status)
		}
		id := loc.Query().Get("req")
		if id == "" {
			return nil, errors.New("sem req em " + loc.String())
		}
		*e.now = e.now.Add(30 * time.Second)
		body := `{"code":"` + e.code(t, secret) + `"}`
		areq, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/oauth/requests/"+id+"/approve", bytes.NewReader([]byte(body)))
		areq.AddCookie(cookie)
		areq.Header.Set("X-CSRF-Token", csrf)
		areq.Header.Set("Content-Type", "application/json")
		aresp, err := browser.Do(areq)
		if err != nil {
			return nil, err
		}
		defer aresp.Body.Close()
		var out struct {
			Redirect string `json:"redirect"`
		}
		if err := decodeBody(aresp.Body, &out); err != nil || aresp.StatusCode != 200 {
			return nil, errors.New("approve falhou: " + aresp.Status)
		}
		ru, err := url.Parse(out.Redirect)
		if err != nil {
			return nil, err
		}
		q := ru.Query()
		return &auth.AuthorizationResult{Code: q.Get("code"), State: q.Get("state"), Iss: q.Get("iss")}, nil
	}

	h, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		RedirectURL: "http://127.0.0.1:1/callback",
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				RedirectURIs: []string{"http://127.0.0.1:1/callback"},
				ClientName:   "Agente E2E",
			},
		},
		AuthorizationCodeFetcher: fetcher,
		Client:                   srv.Client(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:     srv.URL + "/mcp",
		HTTPClient:   srv.Client(),
		OAuthHandler: h,
	}, nil)
	require.NoError(t, err)
	defer session.Close()

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	assert.Contains(t, names, "system_info")

	// A conexão aparece com o nome do cliente.
	toks, err := e.svc.DB.ListAPITokens(ctx, 1)
	require.NoError(t, err)
	require.Len(t, toks, 1)
	assert.True(t, strings.HasPrefix(toks[0].Name, "Agente E2E"), toks[0].Name)
}

func decodeBody(r interface{ Read([]byte) (int, error) }, v any) error {
	return json.NewDecoder(r).Decode(v)
}
