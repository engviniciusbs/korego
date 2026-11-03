package api_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node"
	"github.com/engviniciusbs/korego/internal/node/nodetest"
)

func TestSystem_ReturnsInfo(t *testing.T) {
	e := newEnvWithNode(t, &nodetest.Fake{InfoFn: func(context.Context) (node.HostInfo, error) {
		return node.HostInfo{
			Hostname: "vps.exemplo.com", OS: "linux", Platform: "ubuntu", PlatformVersion: "24.04",
			Kernel: "6.8.0", Arch: "x86_64", CPUs: 4, MemoryTotalBytes: 8 << 30, UptimeSeconds: 3600,
		}, nil
	}})
	_, c, _ := e.setup(t)

	w := e.do(t, http.MethodGet, "/api/system", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	m := decode(t, w)
	assert.Equal(t, "vps.exemplo.com", m["hostname"])
	assert.Equal(t, "linux", m["os"])
	assert.Equal(t, "ubuntu", m["platform"])
	assert.Equal(t, "24.04", m["platformVersion"])
	assert.Equal(t, "6.8.0", m["kernel"])
	assert.Equal(t, "x86_64", m["arch"])
	assert.EqualValues(t, 4, m["cpus"])
	assert.EqualValues(t, 8<<30, m["memoryTotalBytes"])
	assert.EqualValues(t, 3600, m["uptimeSeconds"])
}

func TestSystem_NodeError503(t *testing.T) {
	e := newEnvWithNode(t, &nodetest.Fake{InfoFn: func(context.Context) (node.HostInfo, error) {
		return node.HostInfo{}, errors.New("falha")
	}})
	_, c, _ := e.setup(t)

	w := e.do(t, http.MethodGet, "/api/system", nil, withCookie(c))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	errBody := decode(t, w)["error"].(map[string]any)
	assert.Equal(t, "NODE_UNAVAILABLE", errBody["code"])
	assert.Equal(t, "Não foi possível ler as informações do servidor.", errBody["message"])
}

func TestSystem_RequiresSession(t *testing.T) {
	e := newEnvWithNode(t, &nodetest.Fake{})
	w := e.do(t, http.MethodGet, "/api/system", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
