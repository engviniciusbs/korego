package usage_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/docker/dockertest"
	"github.com/engviniciusbs/korego/internal/node/pm2"
	"github.com/engviniciusbs/korego/internal/node/procs"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/engviniciusbs/korego/internal/usage"
)

var cbase = time.Unix(1_800_000_000, 0).Truncate(time.Minute)

// clock é um relógio controlado pelo teste.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock(t time.Time) *clock { return &clock{t: t} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// fakeProcs devolve sempre a mesma lista e conta as leituras.
type fakeProcs struct {
	mu    sync.Mutex
	ps    []procs.Proc
	calls atomic.Int64
}

func (f *fakeProcs) Snapshot(context.Context) ([]procs.Proc, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]procs.Proc(nil), f.ps...), nil
}

type fakePM2 struct {
	snap pm2.Snapshot
	err  error
}

func (f *fakePM2) Snapshot(context.Context) (pm2.Snapshot, error) { return f.snap, f.err }

func cid(c byte) string { return strings.Repeat(string(c), 64) }

func ctrProc(pid int, id string, cpu float64) procs.Proc {
	return procs.Proc{PID: pid, PPID: 1, Name: "ctr", Cmdline: []string{"ctr"}, Cgroup: "/system.slice/docker-" + id + ".scope", CPUPercent: cpu, RSSBytes: 1 << 20}
}

func svcProc(pid int, unit string, cpu float64, mem uint64) procs.Proc {
	return procs.Proc{PID: pid, PPID: 1, Name: unit, Cmdline: []string{"/usr/bin/" + unit}, Cgroup: "/system.slice/" + unit + ".service", CPUPercent: cpu, RSSBytes: mem}
}

func runC(t *testing.T, c *usage.Collector) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	var once sync.Once
	stop = func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	return stop
}

func waitLatest(t *testing.T, c *usage.Collector) usage.Snapshot {
	t.Helper()
	require.Eventually(t, func() bool { _, ok := c.Latest(); return ok }, 2*time.Second, time.Millisecond)
	s, _ := c.Latest()
	return s
}

// waitReadings espera duas leituras novas: a segunda começou depois da
// chamada e terminou por completo antes de voltar.
func waitReadings(t *testing.T, f *fakeProcs) {
	t.Helper()
	start := f.calls.Load()
	require.Eventually(t, func() bool { return f.calls.Load() >= start+2 }, 2*time.Second, time.Millisecond)
}

func minuteRows(t *testing.T, db *store.DB, minute time.Time) []store.UsageRow {
	t.Helper()
	rows, err := db.UsageAt(context.Background(), minute, minute.Add(time.Minute), time.Minute, "cpu", 1000)
	require.NoError(t, err)
	return rows
}

func byKey(rows []store.UsageRow) map[string]store.UsageRow {
	m := map[string]store.UsageRow{}
	for _, r := range rows {
		m[r.Key] = r
	}
	return m
}

func ownerByKey(os []usage.Owner, key string) (usage.Owner, bool) {
	for _, o := range os {
		if o.Key == key {
			return o, true
		}
	}
	return usage.Owner{}, false
}

func TestCollector_PublishesOwnersWithoutProcesses(t *testing.T) {
	hub := events.NewHub()
	ch, unsub := hub.Subscribe("usage")
	defer unsub()
	idA := cid('a')
	dk := &dockertest.Fake{ListFn: func(context.Context) ([]docker.Container, error) {
		return []docker.Container{{ID: idA, Name: "blog-web-1", Project: "blog", Managed: true}}, nil
	}}
	pp := &fakePM2{snap: pm2.Snapshot{Available: true, DaemonRunning: true, Processes: []pm2.Process{{Name: "api", PID: 100}}}}
	src := &fakeProcs{ps: []procs.Proc{
		ctrProc(10, idA, 5),
		{PID: 100, PPID: 1, Name: "node", Cmdline: []string{"node", "--token=abc", "app.js"}, CPUPercent: 3, RSSBytes: 2 << 20},
		svcProc(20, "nginx", 1, 3<<20),
	}}
	clk := newClock(cbase.Add(5 * time.Second))
	c := &usage.Collector{Procs: src, Docker: dk, PM2: pp, Hub: hub, DB: store.OpenMemory(t), Interval: time.Millisecond, Now: clk.Now}
	runC(t, c)

	var ev events.Event
	select {
	case ev = <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("nada publicado em usage")
	}
	p, ok := ev.Data.(usage.LivePayload)
	require.True(t, ok, "payload deve ser usage.LivePayload, veio %T", ev.Data)
	assert.True(t, p.DockerOK)
	assert.Equal(t, cbase.Add(5*time.Second), p.Time)
	require.Len(t, p.Owners, 3)

	b, err := json.Marshal(p)
	require.NoError(t, err)
	js := string(b)
	assert.NotContains(t, js, "processes")
	assert.NotContains(t, js, "abc")
	var raw struct {
		Time     string           `json:"time"`
		DockerOK bool             `json:"dockerOk"`
		Owners   []map[string]any `json:"owners"`
	}
	require.NoError(t, json.Unmarshal(b, &raw))
	assert.True(t, raw.DockerOK)
	keys := map[string]map[string]any{}
	for _, o := range raw.Owners {
		keys[o["key"].(string)] = o
	}
	require.Contains(t, keys, "project:blog")
	require.Contains(t, keys, "pm2:api")
	require.Contains(t, keys, "service:nginx")
	proj := keys["project:blog"]
	assert.Equal(t, "container", proj["kind"])
	assert.Equal(t, "blog", proj["label"])
	assert.Equal(t, true, proj["managed"])
	assert.Equal(t, 5.0, proj["cpuPercent"])
	assert.Equal(t, float64(1<<20), proj["memBytes"])
	assert.Equal(t, 1.0, proj["procCount"])
	assert.NotContains(t, keys["pm2:api"], "managed")

	// Latest guarda os processos (para a API) e devolve uma cópia.
	s := waitLatest(t, c)
	assert.True(t, s.DockerOK)
	o, ok := ownerByKey(s.Owners, "pm2:api")
	require.True(t, ok)
	require.Len(t, o.Processes, 1)
	assert.Equal(t, 100, o.Processes[0].PID)
	s.Owners[0].Key = "mexido"
	s2, _ := c.Latest()
	assert.NotEqual(t, "mexido", s2.Owners[0].Key)
}

func TestCollector_MinuteTopAndRest(t *testing.T) {
	// 20 serviços: s00..s19. CPU = i+1 → top 15 CPU = s05..s19.
	// RAM: s00 e s01 enormes, os demais (i+1) MiB → top 15 RAM = s00, s01, s07..s19.
	// União = 17 donos; "rest" = s02+s03+s04 = CPU 3+4+5, RAM 3+4+5 MiB.
	const mib = 1 << 20
	var ps []procs.Proc
	for i := 0; i < 20; i++ {
		mem := uint64(i+1) * mib
		if i < 2 {
			mem = uint64(1000+i) * mib
		}
		ps = append(ps, svcProc(100+i, fmt.Sprintf("s%02d", i), float64(i+1), mem))
	}
	db := store.OpenMemory(t)
	clk := newClock(cbase.Add(5 * time.Second))
	src := &fakeProcs{ps: ps}
	c := &usage.Collector{Procs: src, Docker: &dockertest.Fake{}, PM2: &fakePM2{}, Hub: events.NewHub(), DB: db, Interval: time.Millisecond, Now: clk.Now}
	runC(t, c)

	waitLatest(t, c)
	assert.Empty(t, minuteRows(t, db, cbase), "nada gravado antes de virar o minuto")
	clk.Set(cbase.Add(65 * time.Second))

	require.Eventually(t, func() bool { return len(minuteRows(t, db, cbase)) > 0 }, 2*time.Second, 2*time.Millisecond)
	rows := byKey(minuteRows(t, db, cbase))
	require.Len(t, rows, 18)
	for _, i := range []int{0, 1} {
		assert.Contains(t, rows, fmt.Sprintf("service:s%02d", i))
	}
	for i := 5; i < 20; i++ {
		assert.Contains(t, rows, fmt.Sprintf("service:s%02d", i))
	}
	for _, i := range []int{2, 3, 4} {
		assert.NotContains(t, rows, fmt.Sprintf("service:s%02d", i))
	}
	rest := rows["rest"]
	assert.Equal(t, "other", rest.Kind)
	assert.Equal(t, "Demais", rest.Label)
	assert.InDelta(t, 12.0, rest.CPUPercent, 1e-9)
	assert.EqualValues(t, 12*mib, rest.MemBytes)
	s19 := rows["service:s19"]
	assert.InDelta(t, 20.0, s19.CPUPercent, 1e-9)
	assert.Equal(t, "service", s19.Kind)
	assert.Equal(t, "s19", s19.Label)
}

func TestCollector_MinuteAverageAndNoRestWhenAllSelected(t *testing.T) {
	db := store.OpenMemory(t)
	clk := newClock(cbase.Add(5 * time.Second))
	src := &fakeProcs{ps: []procs.Proc{svcProc(1, "a", 10, 100)}}
	c := &usage.Collector{Procs: src, Docker: &dockertest.Fake{}, PM2: &fakePM2{}, DB: db, Interval: time.Millisecond, Now: clk.Now}
	runC(t, c)

	waitReadings(t, src)
	src.mu.Lock()
	src.ps = []procs.Proc{svcProc(1, "a", 30, 300)}
	src.mu.Unlock()
	// Leituras com o novo valor; a média fica entre 10 e 30.
	waitReadings(t, src)
	clk.Set(cbase.Add(61 * time.Second))
	require.Eventually(t, func() bool { return len(minuteRows(t, db, cbase)) > 0 }, 2*time.Second, 2*time.Millisecond)
	rows := byKey(minuteRows(t, db, cbase))
	require.Len(t, rows, 1, "sem donos de fora, não há linha rest")
	a := rows["service:a"]
	assert.Greater(t, a.CPUPercent, 10.0)
	assert.Less(t, a.CPUPercent, 30.0)
	assert.Greater(t, a.MemBytes, uint64(100))
	assert.Less(t, a.MemBytes, uint64(300))
}

func TestCollector_DockerDownGoesToOther(t *testing.T) {
	db := store.OpenMemory(t)
	dk := &dockertest.Fake{ListFn: func(context.Context) ([]docker.Container, error) {
		return nil, docker.ErrUnavailable
	}}
	src := &fakeProcs{ps: []procs.Proc{ctrProc(10, cid('a'), 7), svcProc(20, "nginx", 1, 10)}}
	c := &usage.Collector{Procs: src, Docker: dk, PM2: &fakePM2{}, Hub: events.NewHub(), DB: db, Interval: time.Millisecond, Now: newClock(cbase.Add(time.Second)).Now}
	stop := runC(t, c)

	s := waitLatest(t, c)
	assert.False(t, s.DockerOK)
	o, ok := ownerByKey(s.Owners, "other")
	require.True(t, ok)
	assert.Equal(t, 1, o.ProcCount)
	assert.Equal(t, 7.0, o.CPUPercent)

	stop()
	rows := byKey(minuteRows(t, db, cbase))
	assert.Contains(t, rows, "other")
	assert.Contains(t, rows, "service:nginx")
}

func TestCollector_DockerErrorKeepsCacheFor5Minutes(t *testing.T) {
	idA := cid('a')
	var fail atomic.Bool
	dk := &dockertest.Fake{ListFn: func(context.Context) ([]docker.Container, error) {
		if fail.Load() {
			return nil, errors.New("docker caiu")
		}
		return []docker.Container{{ID: idA, Name: "web", Project: "blog"}}, nil
	}}
	t0 := cbase.Add(time.Second)
	clk := newClock(t0)
	src := &fakeProcs{ps: []procs.Proc{ctrProc(10, idA, 2)}}
	c := &usage.Collector{Procs: src, Docker: dk, PM2: &fakePM2{}, DB: store.OpenMemory(t), Interval: time.Millisecond, Now: clk.Now}
	runC(t, c)

	s := waitLatest(t, c)
	assert.True(t, s.DockerOK)
	_, ok := ownerByKey(s.Owners, "project:blog")
	assert.True(t, ok)

	fail.Store(true)
	clk.Set(t0.Add(30 * time.Second)) // refresh periódico falha
	waitReadings(t, src)
	s, _ = c.Latest()
	assert.False(t, s.DockerOK)
	_, ok = ownerByKey(s.Owners, "project:blog")
	assert.True(t, ok, "mantém o último mapa bom")

	clk.Set(t0.Add(5*time.Minute + time.Second))
	waitReadings(t, src)
	s, _ = c.Latest()
	assert.False(t, s.DockerOK)
	_, ok = ownerByKey(s.Owners, "project:blog")
	assert.False(t, ok, "após 5 min do último sucesso o mapa é esvaziado")
	_, ok = ownerByKey(s.Owners, "other")
	assert.True(t, ok)
}

func TestCollector_UnknownContainerTriggersRefresh(t *testing.T) {
	idA, idB := cid('a'), cid('b')
	var calls atomic.Int64
	dk := &dockertest.Fake{ListFn: func(context.Context) ([]docker.Container, error) {
		n := calls.Add(1)
		cs := []docker.Container{{ID: idA, Name: "a"}}
		if n >= 2 {
			cs = append(cs, docker.Container{ID: idB, Name: "b"})
		}
		return cs, nil
	}}
	t0 := cbase.Add(time.Second)
	clk := newClock(t0)
	src := &fakeProcs{ps: []procs.Proc{ctrProc(10, idA, 1), ctrProc(11, idB, 1)}}
	c := &usage.Collector{Procs: src, Docker: dk, PM2: &fakePM2{}, DB: store.OpenMemory(t), Interval: time.Millisecond, Now: clk.Now}
	runC(t, c)

	waitLatest(t, c)
	waitReadings(t, src)
	assert.EqualValues(t, 1, calls.Load(), "id desconhecido não refaz a lista antes de 5 s")
	s, _ := c.Latest()
	_, ok := ownerByKey(s.Owners, "container:b")
	assert.False(t, ok)

	clk.Set(t0.Add(4 * time.Second))
	waitReadings(t, src)
	assert.EqualValues(t, 1, calls.Load())

	clk.Set(t0.Add(5 * time.Second))
	waitReadings(t, src)
	assert.EqualValues(t, 2, calls.Load(), "id desconhecido refaz a lista após 5 s")
	s, _ = c.Latest()
	_, ok = ownerByKey(s.Owners, "container:b")
	assert.True(t, ok)

	// Tudo conhecido: só o refresh periódico de 30 s.
	clk.Set(t0.Add(20 * time.Second))
	waitReadings(t, src)
	assert.EqualValues(t, 2, calls.Load())
	clk.Set(t0.Add(35 * time.Second))
	waitReadings(t, src)
	assert.EqualValues(t, 3, calls.Load())
}

func TestCollector_PM2ErrorLeavesEmptyMap(t *testing.T) {
	src := &fakeProcs{ps: []procs.Proc{{PID: 100, PPID: 1, Name: "node", CPUPercent: 1}}}
	pp := &fakePM2{snap: pm2.Snapshot{Processes: []pm2.Process{{Name: "api", PID: 100}}}, err: errors.New("pm2 falhou")}
	c := &usage.Collector{Procs: src, Docker: &dockertest.Fake{}, PM2: pp, DB: store.OpenMemory(t), Interval: time.Millisecond, Now: newClock(cbase).Now}
	runC(t, c)
	s := waitLatest(t, c)
	_, ok := ownerByKey(s.Owners, "pm2:api")
	assert.False(t, ok)

	// Daemon parado: também mapa vazio.
	pp2 := &fakePM2{snap: pm2.Snapshot{Available: true, DaemonRunning: false, Processes: []pm2.Process{{Name: "api", PID: 100}}}}
	c2 := &usage.Collector{Procs: src, Docker: &dockertest.Fake{}, PM2: pp2, DB: store.OpenMemory(t), Interval: time.Millisecond, Now: newClock(cbase).Now}
	runC(t, c2)
	s = waitLatest(t, c2)
	_, ok = ownerByKey(s.Owners, "pm2:api")
	assert.False(t, ok)
}

// blockingProcs bloqueia cada leitura até o teste liberar.
type blockingProcs struct {
	release  chan struct{}
	calls    atomic.Int64
	inflight atomic.Int64
	max      atomic.Int64
}

func (b *blockingProcs) Snapshot(ctx context.Context) ([]procs.Proc, error) {
	b.calls.Add(1)
	n := b.inflight.Add(1)
	defer b.inflight.Add(-1)
	for {
		m := b.max.Load()
		if n <= m || b.max.CompareAndSwap(m, n) {
			break
		}
	}
	select {
	case <-b.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return []procs.Proc{svcProc(1, "a", 1, 1)}, nil
}

func TestCollector_NoOverlap(t *testing.T) {
	src := &blockingProcs{release: make(chan struct{})}
	c := &usage.Collector{Procs: src, Docker: &dockertest.Fake{}, PM2: &fakePM2{}, DB: store.OpenMemory(t), Interval: time.Millisecond, Now: newClock(cbase).Now}
	runC(t, c)

	require.Eventually(t, func() bool { return src.calls.Load() == 1 }, 2*time.Second, time.Millisecond)
	time.Sleep(30 * time.Millisecond) // ~30 ticks passam com a leitura presa
	assert.EqualValues(t, 1, src.calls.Load(), "nenhuma leitura nova enquanto a anterior roda")

	src.release <- struct{}{}
	require.Eventually(t, func() bool { return src.calls.Load() == 2 }, 2*time.Second, time.Millisecond)
	src.release <- struct{}{}
	require.Eventually(t, func() bool { return src.calls.Load() == 3 }, 2*time.Second, time.Millisecond)
	assert.EqualValues(t, 1, src.max.Load())
}

func TestCollector_FlushOnStop(t *testing.T) {
	db := store.OpenMemory(t)
	src := &fakeProcs{ps: []procs.Proc{svcProc(1, "a", 40, 4096)}}
	c := &usage.Collector{Procs: src, Docker: &dockertest.Fake{}, PM2: &fakePM2{}, Hub: events.NewHub(), DB: db, Interval: time.Millisecond, Now: newClock(cbase.Add(5 * time.Second)).Now}
	stop := runC(t, c)

	waitLatest(t, c)
	assert.Empty(t, minuteRows(t, db, cbase))
	stop()
	rows := minuteRows(t, db, cbase)
	require.Len(t, rows, 1)
	assert.Equal(t, "service:a", rows[0].Key)
	assert.InDelta(t, 40.0, rows[0].CPUPercent, 1e-9)
	assert.EqualValues(t, 4096, rows[0].MemBytes)
}

func TestContainerID(t *testing.T) {
	id := cid('c')
	// Driver systemd (v2): /system.slice/docker-<id>.scope, às vezes com subgrupo.
	assert.Equal(t, id, usage.ContainerID("/system.slice/docker-"+id+".scope"))
	assert.Equal(t, id, usage.ContainerID("/system.slice/docker-"+id+".scope/init.scope"))
	// Driver cgroupfs: /docker/<id>, tanto na linha "0::" do v2 quanto no v1.
	assert.Equal(t, id, usage.ContainerID("/docker/"+id))
	assert.Equal(t, "", usage.ContainerID("/docker/0123abcd"))
	assert.Equal(t, "", usage.ContainerID("/system.slice/nginx.service"))
}
