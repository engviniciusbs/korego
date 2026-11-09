//go:build integration && linux

package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/docker"
)

const staticITBase = "/var/www/korego-it"

// writeRelease grava releases/<n> com index.html e assets/app.js.
func writeRelease(t *testing.T, n int) {
	t.Helper()
	dir := filepath.Join(staticITBase, "releases", fmt.Sprint(n))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte(fmt.Sprintf("<!doctype html>v%d", n)), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte(fmt.Sprintf("js%d", n)), 0o644))
}

// Review Focus #1: um deploy troca o symlink current enquanto o Caddy serve.
// As requisições continuam funcionando e o conteúdo novo sai sem recarregar o
// Caddy. Também confere o fallback de SPA e os cabeçalhos de cache.
//
// Precisa de Docker com rede host (Linux), das portas 80/443 livres e de
// permissão para criar /var/www/korego-it (root). Sem ela o teste é pulado;
// com KOREGO_IT_STRICT=1 (CI) ele falha.
func TestCaddy_StaticSymlinkSwap_Integration(t *testing.T) {
	ctx := context.Background()
	d := docker.NewLocal()
	require.NoError(t, d.Ping(ctx))

	skip := func(msg string) {
		t.Helper()
		if os.Getenv("KOREGO_IT_STRICT") == "1" {
			t.Fatal("KOREGO_IT_STRICT=1: " + msg)
		}
		t.Skip(msg)
	}
	for _, port := range []int{80, 443} {
		if owner, busy := PortOwner(port); busy {
			skip(fmt.Sprintf("a porta %d já está em uso por %s", port, owner))
		}
	}

	cleanup := func() {
		_ = d.RemoveForce(context.Background(), ContainerName)
		_ = os.RemoveAll(staticITBase)
	}
	_ = d.RemoveForce(ctx, ContainerName)
	_ = os.RemoveAll(staticITBase)
	if err := os.MkdirAll(staticITBase, 0o755); err != nil {
		skip(fmt.Sprintf("não foi possível criar %s (rode como root): %v", staticITBase, err))
	}
	t.Cleanup(cleanup)

	writeRelease(t, 1)
	writeRelease(t, 2)
	current := filepath.Join(staticITBase, "current")
	require.NoError(t, os.Symlink("releases/1", current))

	c := NewCaddy(d, &AdminClient{BaseURL: DefaultAdminURL})
	var st Status
	deadline := time.Now().Add(3 * time.Minute) // inclui o pull da imagem
	for time.Now().Before(deadline) {
		st = c.Ensure(ctx)
		if st.State == StateActive {
			break
		}
		require.Equal(t, StateStarting, st.State, "Ensure: %+v", st)
		time.Sleep(500 * time.Millisecond)
	}
	require.Equal(t, StateActive, st.State, "Caddy não ficou active: %+v", st)

	const host = "static-it.korego.localhost"
	sites := []Site{{
		Host: host,
		Routes: []SiteRoute{{Path: "/", Static: &StaticTarget{
			Root: current, SPA: true, ImmutablePrefix: "/assets/",
		}}},
	}}
	require.NoError(t, c.Apply(ctx, sites, ListenNormal, nil))

	client := &http.Client{Timeout: 5 * time.Second}
	get := func(path string) (int, http.Header, string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:80"+path, nil)
		if err != nil {
			return 0, nil, "", err
		}
		req.Host = host
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header, string(b), nil
	}
	mustGet := func(path string) (int, http.Header, string) {
		t.Helper()
		code, h, body, err := get(path)
		require.NoError(t, err, "GET %s", path)
		return code, h, body
	}

	// Pasta servida de verdade, com SPA e cabeçalhos de cache.
	code, h, body := mustGet("/")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "<!doctype html>v1", body)
	assert.Equal(t, "no-cache", h.Get("Cache-Control"))
	code, h, body = mustGet("/rota/da/spa")
	assert.Equal(t, http.StatusOK, code, "fallback de SPA")
	assert.Equal(t, "<!doctype html>v1", body)
	assert.Equal(t, "no-cache", h.Get("Cache-Control"))
	code, h, body = mustGet("/assets/app.js")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "js1", body)
	assert.Equal(t, "public, max-age=31536000, immutable", h.Get("Cache-Control"))
	code, _, _ = mustGet("/assets/nao-existe.js")
	assert.Equal(t, http.StatusNotFound, code)

	// Requisições contínuas durante a troca: toda resposta é 200 com v1 ou v2.
	var (
		stop     atomic.Bool
		mu       sync.Mutex
		failures []string
		total    int
		wg       sync.WaitGroup
	)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				code, _, body, err := get("/")
				mu.Lock()
				total++
				if err != nil {
					failures = append(failures, err.Error())
				} else if code != http.StatusOK || (body != "<!doctype html>v1" && body != "<!doctype html>v2") {
					failures = append(failures, fmt.Sprintf("%d %q", code, body))
				}
				mu.Unlock()
			}
		}()
	}
	time.Sleep(300 * time.Millisecond)

	// Troca atômica, como num deploy: symlink novo e rename por cima.
	tmp := current + ".novo"
	require.NoError(t, os.Symlink("releases/2", tmp))
	require.NoError(t, os.Rename(tmp, current))

	// A próxima requisição já vê o conteúdo novo, sem recarregar o Caddy.
	code, h, body = mustGet("/")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "<!doctype html>v2", body)
	assert.Equal(t, "no-cache", h.Get("Cache-Control"))
	code, _, body = mustGet("/assets/app.js")
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "js2", body)

	time.Sleep(300 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
	assert.Empty(t, failures, "respostas com erro durante a troca (%d requisições)", total)
	assert.Greater(t, total, 10)

	// A config do Caddy não mudou: nada foi recarregado.
	ok, err := c.InSync(ctx, sites, ListenNormal, nil)
	require.NoError(t, err)
	assert.True(t, ok)
}
