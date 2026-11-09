package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/engviniciusbs/korego/internal/usage"
)

type fakeUsage struct {
	s  usage.Snapshot
	ok bool
}

func (f fakeUsage) Latest() (usage.Snapshot, bool) { return f.s, f.ok }

func TestUsageAPI_Current(t *testing.T) {
	managed := true
	procs := make([]usage.ProcView, 50)
	for i := range procs {
		procs[i] = usage.ProcView{PID: i + 1, Name: "n", Cmd: "node --token=***", User: "root", CPUPercent: 1, MemBytes: 10}
	}
	snap := usage.Snapshot{
		Time:     time.Unix(1_800_000_000, 0),
		DockerOK: true,
		Owners: []usage.Owner{
			{Ref: usage.Ref{Key: "container:abc", Label: "web", Kind: usage.KindContainer, Managed: &managed},
				CPUPercent: 12.5, MemBytes: 1024, ProcCount: 80, Processes: procs, Truncated: true},
			{Ref: usage.Ref{Key: "kernel", Label: "Kernel", Kind: usage.KindKernel}, ProcCount: 1,
				Processes: []usage.ProcView{{PID: 2, Name: "kthreadd"}}},
		},
	}
	e := newEnvDeps(t, api.Deps{Usage: fakeUsage{s: snap, ok: true}, DB: store.OpenMemory(t)})
	_, c, _ := e.setup(t)

	w := e.do(t, http.MethodGet, "/api/usage", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	m := decode(t, w)
	assert.Equal(t, true, m["dockerOk"])
	assert.Contains(t, m, "time")
	owners := m["owners"].([]any)
	require.Len(t, owners, 2)
	o := owners[0].(map[string]any)
	assert.Equal(t, "container:abc", o["key"])
	assert.Equal(t, "container", o["kind"])
	assert.Equal(t, "web", o["label"])
	assert.Equal(t, true, o["managed"])
	assert.EqualValues(t, 12.5, o["cpuPercent"])
	assert.EqualValues(t, 1024, o["memBytes"])
	assert.EqualValues(t, 80, o["procCount"])
	assert.Equal(t, true, o["truncated"])
	ps := o["processes"].([]any)
	assert.Len(t, ps, 50)
	p := ps[0].(map[string]any)
	assert.Equal(t, "node --token=***", p["cmd"])
	assert.Equal(t, "root", p["user"])
	assert.EqualValues(t, 1, p["pid"])
	assert.Equal(t, "n", p["name"])
	assert.NotContains(t, owners[1].(map[string]any), "managed")
}

func TestUsageAPI_NotReady(t *testing.T) {
	for name, d := range map[string]api.Deps{
		"sem snapshot": {Usage: fakeUsage{}},
		"nil":          {},
	} {
		d.DB = store.OpenMemory(t)
		e := newEnvDeps(t, d)
		_, c, _ := e.setup(t)
		w := e.do(t, http.MethodGet, "/api/usage", nil, withCookie(c))
		require.Equal(t, http.StatusServiceUnavailable, w.Code, name)
		assert.Equal(t, "USAGE_NOT_READY", errCode(decode(t, w)), name)
	}
}

func TestUsageAPI_AtBucketAndEmpty(t *testing.T) {
	db := store.OpenMemory(t)
	base := int64(1_800_000_000) - 1_800_000_000%1800 // início de um balde de 30 min
	var rows []store.UsageRow
	for i := int64(0); i < 30; i++ {
		rows = append(rows,
			store.UsageRow{TS: base + i*60, Key: "project:1", Kind: "project", Label: "loja", CPUPercent: float64(i), MemBytes: 100},
			store.UsageRow{TS: base + i*60, Key: "other", Kind: "other", Label: "Outros", CPUPercent: 1, MemBytes: 900},
		)
	}
	// Linha fora do balde, não pode entrar na média.
	rows = append(rows, store.UsageRow{TS: base + 1800, Key: "project:1", Kind: "project", Label: "loja", CPUPercent: 99, MemBytes: 1})
	require.NoError(t, db.InsertUsageMinute(context.Background(), rows))
	now := time.Unix(base+7200, 0)
	e := newEnvDeps(t, api.Deps{Usage: fakeUsage{}, DB: db, Now: func() time.Time { return now }})
	_, c, _ := e.setup(t)

	w := e.do(t, http.MethodGet, fmt.Sprintf("/api/usage/at?t=%d&range=7d", base+17*60), nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	m := decode(t, w)
	assert.EqualValues(t, base, m["t"])
	assert.EqualValues(t, 1800, m["stepSeconds"])
	assert.Equal(t, "cpu", m["metric"])
	owners := m["owners"].([]any)
	require.Len(t, owners, 2)
	o := owners[0].(map[string]any)
	assert.Equal(t, "project:1", o["key"])
	assert.Equal(t, "project", o["kind"])
	assert.Equal(t, "loja", o["label"])
	assert.EqualValues(t, 14.5, o["cpuPercent"])
	assert.EqualValues(t, 100, o["memBytes"])

	w = e.do(t, http.MethodGet, fmt.Sprintf("/api/usage/at?t=%d&range=7d&metric=mem", base+17*60), nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code)
	m = decode(t, w)
	assert.Equal(t, "mem", m["metric"])
	assert.Equal(t, "other", m["owners"].([]any)[0].(map[string]any)["key"])

	w = e.do(t, http.MethodGet, fmt.Sprintf("/api/usage/at?t=%d&range=1h", base-86400), nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []any{}, decode(t, w)["owners"])
}

func TestUsageAPI_AtLimit10(t *testing.T) {
	db := store.OpenMemory(t)
	var rows []store.UsageRow
	for i := 0; i < 15; i++ {
		rows = append(rows, store.UsageRow{TS: 1_799_999_940, Key: fmt.Sprintf("pm2:a%02d", i), Kind: "pm2", Label: "a", CPUPercent: float64(i)})
	}
	require.NoError(t, db.InsertUsageMinute(context.Background(), rows))
	e := newEnvDeps(t, api.Deps{DB: db, Now: func() time.Time { return time.Unix(1_800_000_000, 0) }})
	_, c, _ := e.setup(t)
	w := e.do(t, http.MethodGet, "/api/usage/at?t=1799999940&range=1h", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Len(t, decode(t, w)["owners"], 10)
}

func TestUsageAPI_Validation(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	e := newEnvDeps(t, api.Deps{DB: store.OpenMemory(t), Now: func() time.Time { return now }})
	_, c, _ := e.setup(t)

	cases := []struct{ url, code string }{
		{"/api/usage/at?range=1h", "INVALID_TIME"},
		{"/api/usage/at?t=abc&range=1h", "INVALID_TIME"},
		{"/api/usage/at?t=1799999000.5&range=1h", "INVALID_TIME"},
		{"/api/usage/at?t=1800000061&range=1h", "INVALID_TIME"},
		{"/api/usage/at?t=1799999000&range=2h", "INVALID_RANGE"},
		{"/api/usage/at?t=1799999000", "INVALID_RANGE"},
		{"/api/usage/at?t=1799999000&range=1h&metric=disk", "INVALID_METRIC"},
		{"/api/usage/history?range=1h", "INVALID_OWNER"},
		{"/api/usage/history?owner=foo&range=1h", "INVALID_OWNER"},
		{"/api/usage/history?owner=project:&range=1h", "INVALID_OWNER"},
		{"/api/usage/history?owner=other:x&range=1h", "INVALID_OWNER"},
		{"/api/usage/history?owner=project:" + strings.Repeat("a", 200) + "&range=1h", "INVALID_OWNER"},
		{"/api/usage/history?owner=kernel&range=2h", "INVALID_RANGE"},
	}
	for _, tc := range cases {
		w := e.do(t, http.MethodGet, tc.url, nil, withCookie(c))
		require.Equal(t, http.StatusBadRequest, w.Code, tc.url)
		assert.Equal(t, tc.code, errCode(decode(t, w)), tc.url)
	}
	// Válidos: até 60 s no futuro, e as chaves sem argumento.
	for _, u := range []string{
		"/api/usage/at?t=1800000060&range=1h",
		"/api/usage/history?owner=session&range=1h",
		"/api/usage/history?owner=rest&range=24h",
		"/api/usage/history?owner=container:abc&range=7d",
	} {
		w := e.do(t, http.MethodGet, u, nil, withCookie(c))
		assert.Equal(t, http.StatusOK, w.Code, u+" "+w.Body.String())
	}
}

func TestUsageAPI_History(t *testing.T) {
	db := store.OpenMemory(t)
	now := time.Unix(1_800_000_000, 0)
	ts := now.Add(-30 * time.Minute).Truncate(time.Minute).Unix()
	require.NoError(t, db.InsertUsageMinute(context.Background(), []store.UsageRow{
		{TS: ts, Key: "pm2:api", Kind: "pm2", Label: "api", CPUPercent: 40, MemBytes: 2048},
		{TS: ts, Key: "other", Kind: "other", Label: "Outros", CPUPercent: 7, MemBytes: 1},
	}))
	e := newEnvDeps(t, api.Deps{DB: db, Now: func() time.Time { return now }})
	_, c, _ := e.setup(t)

	for rng, step := range map[string]int{"1h": 60, "24h": 300, "7d": 1800} {
		w := e.do(t, http.MethodGet, "/api/usage/history?owner=pm2:api&range="+rng, nil, withCookie(c))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		m := decode(t, w)
		assert.EqualValues(t, step, m["stepSeconds"], rng)
		pts := m["points"].([]any)
		require.Len(t, pts, 1, rng)
		p := pts[0].(map[string]any)
		assert.EqualValues(t, ts-ts%int64(step), p["t"])
		assert.EqualValues(t, 40, p["cpuPercent"])
		assert.EqualValues(t, 2048, p["memBytes"])
	}

	w := e.do(t, http.MethodGet, "/api/usage/history?owner=session&range=1h", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []any{}, decode(t, w)["points"])
}

func TestUsageAPI_RequireSession(t *testing.T) {
	e := newEnvDeps(t, api.Deps{DB: store.OpenMemory(t)})
	for _, u := range []string{"/api/usage", "/api/usage/at?t=1&range=1h", "/api/usage/history?owner=other&range=1h"} {
		assert.Equal(t, http.StatusUnauthorized, e.do(t, http.MethodGet, u, nil).Code, u)
	}
}
