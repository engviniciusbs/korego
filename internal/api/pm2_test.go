package api_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/node/metrics"
	"github.com/engviniciusbs/korego/internal/node/nodetest"
	"github.com/engviniciusbs/korego/internal/node/pm2"
	"github.com/engviniciusbs/korego/internal/store"
)

type fakePM2 struct {
	snap pm2.Snapshot
	err  error
}

func (f fakePM2) Snapshot(context.Context) (pm2.Snapshot, error) { return f.snap, f.err }

func pm2Env(t *testing.T, r pm2.Reader) (*env, *http.Cookie) {
	t.Helper()
	e := newEnvDeps(t, api.Deps{
		Node:    &nodetest.Fake{PM2Reader: r},
		Metrics: fakeLatest{s: metrics.Sample{CPUPercent: 1}, ok: true},
		DB:      store.OpenMemory(t),
	})
	_, c, _ := e.setup(t)
	return e, c
}

func TestPM2API(t *testing.T) {
	e, c := pm2Env(t, fakePM2{snap: pm2.Snapshot{
		Available: true, DaemonRunning: true,
		Processes: []pm2.Process{{Name: "api-exemplo", PMID: 0, Status: "online", Restarts: 2, UptimeSeconds: 90}},
	}})
	w := e.do(t, http.MethodGet, "/api/pm2", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := decode(t, w)
	assert.Equal(t, true, body["available"])
	assert.Equal(t, true, body["daemonRunning"])
	ps := body["processes"].([]any)
	require.Len(t, ps, 1)
	assert.Equal(t, "api-exemplo", ps[0].(map[string]any)["name"])
	assert.Equal(t, "online", ps[0].(map[string]any)["status"])
}

func TestPM2API_Error503(t *testing.T) {
	e, c := pm2Env(t, fakePM2{err: errors.New("falhou")})
	w := e.do(t, http.MethodGet, "/api/pm2", nil, withCookie(c))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "PM2_UNAVAILABLE", decode(t, w)["error"].(map[string]any)["code"])
}

func TestPM2API_RequiresSession(t *testing.T) {
	e, _ := pm2Env(t, fakePM2{})
	w := e.do(t, http.MethodGet, "/api/pm2", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
