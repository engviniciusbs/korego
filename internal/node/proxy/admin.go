package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultAdminURL é o endereço padrão da Admin API do Caddy.
const DefaultAdminURL = "http://127.0.0.1:2019"

const (
	defaultAdminTimeout = 10 * time.Second
	maxErrorBody        = 2 * 1024
	maxConfigBody       = 16 << 20
)

var defaultAdminHTTP = &http.Client{Timeout: defaultAdminTimeout}

// AdminClient fala com a Admin API do Caddy. Campos vazios usam os padrões
// (DefaultAdminURL e um cliente HTTP com timeout de 10 s).
type AdminClient struct {
	BaseURL string
	HTTP    *http.Client
}

// NewAdminClient devolve um cliente com os padrões preenchidos.
func NewAdminClient() *AdminClient {
	return &AdminClient{BaseURL: DefaultAdminURL, HTTP: &http.Client{Timeout: defaultAdminTimeout}}
}

func (c *AdminClient) baseURL() string {
	if c.BaseURL == "" {
		return DefaultAdminURL
	}
	return strings.TrimRight(c.BaseURL, "/")
}

func (c *AdminClient) httpClient() *http.Client {
	if c.HTTP == nil {
		return defaultAdminHTTP
	}
	return c.HTTP
}

// Load substitui a configuração inteira do Caddy (POST /load).
func (c *AdminClient) Load(ctx context.Context, cfg []byte) error {
	_, err := c.do(ctx, http.MethodPost, "/load", cfg)
	return err
}

// Config devolve a configuração atual (GET /config/). Um Caddy sem config
// responde "null".
func (c *AdminClient) Config(ctx context.Context) ([]byte, error) {
	return c.do(ctx, http.MethodGet, "/config/", nil)
}

// Ping confere se a Admin API responde.
func (c *AdminClient) Ping(ctx context.Context) error {
	_, err := c.Config(ctx)
	return err
}

func (c *AdminClient) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL()+path, rd)
	if err != nil {
		return nil, fmt.Errorf("caddy admin %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("caddy admin %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, fmt.Errorf("caddy admin %s %s: status %d: %s",
			method, path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	out, err := io.ReadAll(io.LimitReader(resp.Body, maxConfigBody))
	if err != nil {
		return nil, fmt.Errorf("caddy admin %s %s: lendo resposta: %w", method, path, err)
	}
	return out, nil
}
