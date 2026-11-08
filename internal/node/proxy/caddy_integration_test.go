//go:build integration && linux

package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/docker"
)

// Precisa de Docker com rede host (Linux) e das portas 80/443 livres.
func TestCaddy_Integration(t *testing.T) {
	ctx := context.Background()
	d := docker.NewLocal()
	require.NoError(t, d.Ping(ctx))
	require.NoError(t, d.RemoveForce(ctx, ContainerName))
	t.Cleanup(func() { _ = d.RemoveForce(context.Background(), ContainerName) })

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "korego-upstream")
	}))
	defer upstream.Close()

	c := NewCaddy(d, &AdminClient{BaseURL: DefaultAdminURL})
	waitActive := func() {
		t.Helper()
		var st Status
		deadline := time.Now().Add(3 * time.Minute) // inclui o pull da imagem
		for time.Now().Before(deadline) {
			st = c.Ensure(ctx)
			if st.State == StateActive {
				return
			}
			require.Equal(t, StateStarting, st.State, "Ensure: %+v", st)
			time.Sleep(500 * time.Millisecond)
		}
		t.Fatalf("Caddy não ficou active: %+v", st)
	}
	waitActive()

	routes := []Route{{
		Host:     "teste.korego.localhost",
		Upstream: strings.TrimPrefix(upstream.URL, "http://"),
	}}
	require.NoError(t, c.Apply(ctx, routes))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:80/", nil)
	require.NoError(t, err)
	req.Host = "teste.korego.localhost"
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "korego-upstream", string(body))

	ok, err := c.InSync(ctx, routes)
	require.NoError(t, err)
	assert.True(t, ok)

	// Reiniciado, o Caddy volta sem config (caddy run sem --resume).
	out, err := exec.Command("docker", "restart", ContainerName).CombinedOutput()
	require.NoError(t, err, string(out))
	waitActive()
	ok, err = c.InSync(ctx, routes)
	require.NoError(t, err)
	assert.False(t, ok)

	require.NoError(t, c.Apply(ctx, routes))
	ok, err = c.InSync(ctx, routes)
	require.NoError(t, err)
	assert.True(t, ok)
}
