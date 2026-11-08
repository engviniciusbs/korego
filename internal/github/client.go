package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	maxErrBody  = 2 << 10
	maxRespBody = 8 << 20
	maxPages    = 50
)

var codeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

// Client fala com a API do GitHub. Os zero values usam o GitHub público.
type Client struct {
	APIURL string
	WebURL string
	HTTP   *http.Client
	Now    func() time.Time
}

// Conversion é o resultado da conversão do manifesto.
type Conversion struct {
	ID            int64
	Slug          string
	PEM           string
	WebhookSecret string
	HTMLURL       string
	Owner         string
}

// Installation é uma instalação do App.
type Installation struct {
	ID      int64
	Account string
}

// Repo é um repositório visível a uma instalação.
type Repo struct {
	FullName      string
	DefaultBranch string
	Private       bool
}

func (c *Client) apiURL() string {
	if c.APIURL == "" {
		return "https://api.github.com"
	}
	return strings.TrimRight(c.APIURL, "/")
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// do executa a requisição; auth é o valor de Authorization (vazio = sem). Em
// erro HTTP devolve só status e mensagem do GitHub, nunca credenciais.
func (c *Client) do(ctx context.Context, method, rawURL, auth string, out any) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("github: montar requisição: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "KoreGO")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	// O code do manifesto troca-se pela chave privada: nunca aparece em erros.
	path := req.URL.Path
	if strings.HasPrefix(path, "/app-manifests/") {
		path = "/app-manifests/***/conversions"
	}
	resp, err := c.http().Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("github: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(bytes.TrimSpace(body), &e)
		if e.Message == "" {
			return nil, fmt.Errorf("github: %s %s: status %d", method, path, resp.StatusCode)
		}
		return nil, fmt.Errorf("github: %s %s: status %d: %s", method, path, resp.StatusCode, e.Message)
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxRespBody)).Decode(out); err != nil {
			return nil, fmt.Errorf("github: resposta inválida de %s: %w", path, err)
		}
	}
	return resp.Header, nil
}

// ConvertManifest troca o code do fluxo de manifesto pelas credenciais do App.
func (c *Client) ConvertManifest(ctx context.Context, code string) (Conversion, error) {
	if !codeRe.MatchString(code) {
		return Conversion{}, errors.New("github: code inválido")
	}
	var r struct {
		ID            int64  `json:"id"`
		Slug          string `json:"slug"`
		PEM           string `json:"pem"`
		WebhookSecret string `json:"webhook_secret"`
		HTMLURL       string `json:"html_url"`
		Owner         struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if _, err := c.do(ctx, http.MethodPost, c.apiURL()+"/app-manifests/"+code+"/conversions", "", &r); err != nil {
		return Conversion{}, err
	}
	return Conversion{ID: r.ID, Slug: r.Slug, PEM: r.PEM, WebhookSecret: r.WebhookSecret, HTMLURL: r.HTMLURL, Owner: r.Owner.Login}, nil
}

// Installations lista as instalações do App (autenticado com o JWT).
func (c *Client) Installations(ctx context.Context, jwt string) ([]Installation, error) {
	var r []struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
	}
	if _, err := c.do(ctx, http.MethodGet, c.apiURL()+"/app/installations?per_page=100", "Bearer "+jwt, &r); err != nil {
		return nil, err
	}
	out := make([]Installation, 0, len(r))
	for _, i := range r {
		out = append(out, Installation{ID: i.ID, Account: i.Account.Login})
	}
	return out, nil
}

// InstallationToken cria um token de instalação.
func (c *Client) InstallationToken(ctx context.Context, jwt string, installationID int64) (string, time.Time, error) {
	var r struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	u := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.apiURL(), installationID)
	if _, err := c.do(ctx, http.MethodPost, u, "Bearer "+jwt, &r); err != nil {
		return "", time.Time{}, err
	}
	if r.Token == "" {
		return "", time.Time{}, errors.New("github: resposta sem token")
	}
	return r.Token, r.ExpiresAt, nil
}

// Repos lista os repositórios da instalação do token, seguindo o cabeçalho Link.
func (c *Client) Repos(ctx context.Context, token string) ([]Repo, error) {
	var out []Repo
	next := c.apiURL() + "/installation/repositories?per_page=100"
	for page := 0; next != "" && page < maxPages; page++ {
		var r struct {
			Repositories []struct {
				FullName      string `json:"full_name"`
				DefaultBranch string `json:"default_branch"`
				Private       bool   `json:"private"`
			} `json:"repositories"`
		}
		h, err := c.do(ctx, http.MethodGet, next, "Bearer "+token, &r)
		if err != nil {
			return nil, err
		}
		for _, x := range r.Repositories {
			out = append(out, Repo{FullName: x.FullName, DefaultBranch: x.DefaultBranch, Private: x.Private})
		}
		next = c.nextLink(h.Get("Link"))
	}
	return out, nil
}

// nextLink extrai rel="next" do cabeçalho Link; só aceita URLs sob APIURL.
func (c *Client) nextLink(h string) string {
	for _, part := range strings.Split(h, ",") {
		seg := strings.Split(part, ";")
		if len(seg) < 2 {
			continue
		}
		isNext := false
		for _, p := range seg[1:] {
			if strings.ReplaceAll(strings.TrimSpace(p), " ", "") == `rel="next"` {
				isNext = true
			}
		}
		u := strings.TrimSpace(seg[0])
		if !isNext || len(u) < 3 || u[0] != '<' || u[len(u)-1] != '>' {
			continue
		}
		u = u[1 : len(u)-1]
		if strings.HasPrefix(u, c.apiURL()+"/") {
			return u
		}
	}
	return ""
}
