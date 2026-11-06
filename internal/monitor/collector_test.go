package monitor_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/monitor"
	"github.com/engviniciusbs/korego/internal/node/metrics"
	"github.com/engviniciusbs/korego/internal/store"
)

// step é uma resposta programada da fonte falsa.
type step struct {
	s   metrics.Sample
	err error
}

// seqSource devolve as respostas em ordem e repete a última ao esgotar.
type seqSource struct {
	mu    sync.Mutex
	steps []step
	i     int
}

func (f *seqSource) Sample(context.Context) (metrics.Sample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.steps[f.i]
	if f.i < len(f.steps)-1 {
		f.i++
	}
	return st.s, st.err
}

var base = time.Unix(1_800_000_000, 0).Truncate(time.Minute)

func smp(offset time.Duration, cpu float64) metrics.Sample {
	return metrics.Sample{
		Time: base.Add(offset), CPUPercent: cpu,
		MemUsedBytes: 100, MemTotalBytes: 200,
		Disks: []metrics.Disk{{Mount: "/", UsedBytes: 10, TotalBytes: 20}, {Mount: "/b", UsedBytes: 5, TotalBytes: 30}},
	}
}

func run(t *testing.T, c *monitor.Collector) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	var once sync.Once
	stop = func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	return stop
}

func history(t *testing.T, db *store.DB) []store.MetricMinute {
	t.Helper()
	pts, err := db.MetricHistory(context.Background(), base.Add(-time.Hour), base.Add(time.Hour), time.Minute)
	require.NoError(t, err)
	return pts
}

func fixedNow() time.Time { return base }

func TestCollector_PublishesSamples(t *testing.T) {
	hub := events.NewHub()
	ch, cancel := hub.Subscribe("metrics")
	defer cancel()
	c := &monitor.Collector{
		Source: &seqSource{steps: []step{{s: smp(0, 10)}}}, Hub: hub, DB: store.OpenMemory(t),
		Interval: time.Millisecond, Now: fixedNow,
	}
	run(t, c)

	select {
	case ev := <-ch:
		s, ok := ev.Data.(metrics.Sample)
		require.True(t, ok)
		assert.Equal(t, 10.0, s.CPUPercent)
	case <-time.After(2 * time.Second):
		t.Fatal("nenhuma amostra publicada")
	}
	require.Eventually(t, func() bool { _, ok := c.Latest(); return ok }, 2*time.Second, time.Millisecond)
}

func TestCollector_WritesMinuteAverage(t *testing.T) {
	db := store.OpenMemory(t)
	c := &monitor.Collector{
		Source: &seqSource{steps: []step{
			{s: smp(5*time.Second, 10)}, {s: smp(10*time.Second, 30)}, {s: smp(65*time.Second, 90)},
		}},
		Hub: events.NewHub(), DB: db, Interval: time.Millisecond, Now: fixedNow,
	}
	run(t, c)

	require.Eventually(t, func() bool { return len(history(t, db)) > 0 }, 2*time.Second, 5*time.Millisecond)
	pts := history(t, db)
	assert.Equal(t, base.Unix(), pts[0].TS)
	assert.InDelta(t, 20.0, pts[0].CPUPercent, 0.001)
	assert.EqualValues(t, 100, pts[0].MemUsed)
	assert.EqualValues(t, 15, pts[0].DiskUsed)
	assert.EqualValues(t, 50, pts[0].DiskTotal)
}

func TestCollector_FlushesOnStop(t *testing.T) {
	db := store.OpenMemory(t)
	c := &monitor.Collector{
		Source: &seqSource{steps: []step{{s: smp(5*time.Second, 40)}}},
		Hub:    events.NewHub(), DB: db, Interval: time.Millisecond, Now: fixedNow,
	}
	stop := run(t, c)
	require.Eventually(t, func() bool { _, ok := c.Latest(); return ok }, 2*time.Second, time.Millisecond)
	assert.Empty(t, history(t, db))

	stop()
	pts := history(t, db)
	require.Len(t, pts, 1)
	assert.InDelta(t, 40.0, pts[0].CPUPercent, 0.001)
}

func TestCollector_SourceErrorKeepsRunning(t *testing.T) {
	hub := events.NewHub()
	ch, cancel := hub.Subscribe("metrics")
	defer cancel()
	c := &monitor.Collector{
		Source: &seqSource{steps: []step{{err: errors.New("falha")}, {s: smp(0, 7)}}},
		Hub:    hub, DB: store.OpenMemory(t), Interval: time.Millisecond, Now: fixedNow,
	}
	run(t, c)

	select {
	case ev := <-ch:
		assert.Equal(t, 7.0, ev.Data.(metrics.Sample).CPUPercent)
	case <-time.After(2 * time.Second):
		t.Fatal("coletor parou após erro da fonte")
	}
}

func TestCollector_CleansOldRows(t *testing.T) {
	db := store.OpenMemory(t)
	old := base.Add(-8 * 24 * time.Hour)
	require.NoError(t, db.UpsertMetricMinute(context.Background(), store.MetricMinute{TS: old.Unix(), CPUPercent: 1}))
	c := &monitor.Collector{
		Source: &seqSource{steps: []step{{err: errors.New("sem dados")}}},
		Hub:    events.NewHub(), DB: db, Interval: time.Millisecond, Now: fixedNow,
	}
	run(t, c)

	require.Eventually(t, func() bool {
		pts, err := db.MetricHistory(context.Background(), old.Add(-time.Hour), old.Add(time.Hour), time.Minute)
		return err == nil && len(pts) == 0
	}, 2*time.Second, 5*time.Millisecond)
}
