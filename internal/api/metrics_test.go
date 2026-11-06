package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/node/metrics"
	"github.com/engviniciusbs/korego/internal/store"
)

type fakeLatest struct {
	s  metrics.Sample
	ok bool
}

func (f fakeLatest) Latest() (metrics.Sample, bool) { return f.s, f.ok }

func TestMetricsCurrent_NotReady503(t *testing.T) {
	e := newEnvDeps(t, api.Deps{Metrics: fakeLatest{}, DB: store.OpenMemory(t)})
	_, c, _ := e.setup(t)

	w := e.do(t, http.MethodGet, "/api/metrics/current", nil, withCookie(c))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "METRICS_NOT_READY", decode(t, w)["error"].(map[string]any)["code"])
}

func TestMetricsCurrent_ReturnsSample(t *testing.T) {
	e := newEnvDeps(t, api.Deps{
		Metrics: fakeLatest{s: metrics.Sample{CPUPercent: 12.5, MemTotalBytes: 99}, ok: true},
		DB:      store.OpenMemory(t),
	})
	_, c, _ := e.setup(t)

	w := e.do(t, http.MethodGet, "/api/metrics/current", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	m := decode(t, w)
	assert.EqualValues(t, 12.5, m["cpuPercent"])
	assert.EqualValues(t, 99, m["memTotalBytes"])
}

func TestMetricsHistory_Ranges(t *testing.T) {
	db := store.OpenMemory(t)
	now := time.Unix(1_800_000_000, 0)
	ts := now.Add(-30 * time.Minute).Truncate(time.Minute).Unix()
	require.NoError(t, db.UpsertMetricMinute(context.Background(), store.MetricMinute{
		TS: ts, CPUPercent: 42, MemUsed: 1, MemTotal: 2, Load1: 0.5, NetRx: 3, NetTx: 4, DiskUsed: 5, DiskTotal: 6,
	}))
	e := newEnvDeps(t, api.Deps{Metrics: fakeLatest{}, DB: db, Now: func() time.Time { return now }})
	_, c, _ := e.setup(t)

	for rng, step := range map[string]int{"1h": 60, "24h": 300, "7d": 1800} {
		w := e.do(t, http.MethodGet, "/api/metrics/history?range="+rng, nil, withCookie(c))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		m := decode(t, w)
		assert.Equal(t, rng, m["range"])
		assert.EqualValues(t, step, m["stepSeconds"])
		pts := m["points"].([]any)
		require.Len(t, pts, 1, rng)
		p := pts[0].(map[string]any)
		assert.EqualValues(t, 42, p["cpuPercent"])
		assert.EqualValues(t, 3, p["netRxBytesPerSec"])
		assert.EqualValues(t, 4, p["netTxBytesPerSec"])
		assert.EqualValues(t, 6, p["diskTotalBytes"])
		assert.EqualValues(t, 5, p["diskUsedBytes"])
		assert.EqualValues(t, 2, p["memTotalBytes"])
		assert.EqualValues(t, 1, p["memUsedBytes"])
		assert.EqualValues(t, 0.5, p["load1"])
		assert.Contains(t, p, "t")
	}

	w := e.do(t, http.MethodGet, "/api/metrics/history?range=2h", nil, withCookie(c))
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "INVALID_RANGE", decode(t, w)["error"].(map[string]any)["code"])
}

func TestMetricsHistory_EmptyIsArray(t *testing.T) {
	e := newEnvDeps(t, api.Deps{Metrics: fakeLatest{}, DB: store.OpenMemory(t)})
	_, c, _ := e.setup(t)
	w := e.do(t, http.MethodGet, "/api/metrics/history?range=1h", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []any{}, decode(t, w)["points"])
}

func TestMetrics_RequireSession(t *testing.T) {
	e := newEnvDeps(t, api.Deps{Metrics: fakeLatest{}, DB: store.OpenMemory(t)})
	assert.Equal(t, http.StatusUnauthorized, e.do(t, http.MethodGet, "/api/metrics/current", nil).Code)
	assert.Equal(t, http.StatusUnauthorized, e.do(t, http.MethodGet, "/api/metrics/history?range=1h", nil).Code)
}
