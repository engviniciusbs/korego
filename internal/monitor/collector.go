// Package monitor coleta métricas do host periodicamente.
package monitor

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/node/metrics"
	"github.com/engviniciusbs/korego/internal/store"
)

const (
	defaultInterval  = 2 * time.Second
	defaultRetention = 7 * 24 * time.Hour
	cleanupEvery     = 24 * time.Hour
)

// Collector lê amostras da fonte, publica no hub (tópico "metrics") e grava a
// média por minuto no banco.
type Collector struct {
	Source    metrics.Source
	Hub       *events.Hub
	DB        *store.DB
	Interval  time.Duration    // 0 = 2s
	Retention time.Duration    // 0 = 7 * 24h
	Now       func() time.Time // nil = time.Now

	mu     sync.RWMutex
	latest metrics.Sample
	has    bool
}

// Latest devolve a última amostra coletada. Seguro para uso concorrente.
func (c *Collector) Latest() (metrics.Sample, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.latest, c.has
}

// acc acumula as amostras de um minuto.
type acc struct {
	minute              time.Time
	n                   int
	cpu, load1, rx, tx  float64
	memUsed, memTotal   float64
	diskUsed, diskTotal float64
}

func (a *acc) add(s metrics.Sample) {
	a.n++
	a.cpu += s.CPUPercent
	a.load1 += s.Load1
	a.rx += s.NetRxBytesPerSec
	a.tx += s.NetTxBytesPerSec
	a.memUsed += float64(s.MemUsedBytes)
	a.memTotal += float64(s.MemTotalBytes)
	for _, d := range s.Disks {
		a.diskUsed += float64(d.UsedBytes)
		a.diskTotal += float64(d.TotalBytes)
	}
}

func (a *acc) minuteRow() store.MetricMinute {
	n := float64(a.n)
	return store.MetricMinute{
		TS:         a.minute.Unix(),
		CPUPercent: a.cpu / n,
		MemUsed:    uint64(a.memUsed/n + 0.5),
		MemTotal:   uint64(a.memTotal/n + 0.5),
		Load1:      a.load1 / n,
		NetRx:      a.rx / n,
		NetTx:      a.tx / n,
		DiskUsed:   uint64(a.diskUsed/n + 0.5),
		DiskTotal:  uint64(a.diskTotal/n + 0.5),
	}
}

func (c *Collector) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Run coleta até ctx terminar; ao sair grava o minuto parcial.
func (c *Collector) Run(ctx context.Context) {
	interval := c.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	retention := c.Retention
	if retention <= 0 {
		retention = defaultRetention
	}

	c.cleanup(ctx, retention)

	var cur *acc
	flush := func(fctx context.Context) {
		if cur == nil || cur.n == 0 {
			return
		}
		if err := c.DB.UpsertMetricMinute(fctx, cur.minuteRow()); err != nil {
			log.Printf("gravar métricas do minuto: %v", err)
		}
		cur = nil
	}
	defer func() {
		// ctx já terminou: grava o minuto parcial com um contexto próprio.
		fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		flush(fctx)
	}()

	tick := time.NewTicker(interval)
	defer tick.Stop()
	daily := time.NewTicker(cleanupEvery)
	defer daily.Stop()

	for {
		s, err := c.Source.Sample(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			log.Printf("coletar métricas: %v", err)
		default:
			c.mu.Lock()
			c.latest, c.has = s, true
			c.mu.Unlock()
			if c.Hub != nil {
				c.Hub.Publish("metrics", s)
			}
			m := s.Time.Truncate(time.Minute)
			if cur != nil && !cur.minute.Equal(m) {
				flush(ctx)
			}
			if cur == nil {
				cur = &acc{minute: m}
			}
			cur.add(s)
		}

		select {
		case <-ctx.Done():
			return
		case <-daily.C:
			c.cleanup(ctx, retention)
		case <-tick.C:
		}
	}
}

func (c *Collector) cleanup(ctx context.Context, retention time.Duration) {
	if _, err := c.DB.DeleteMetricsBefore(ctx, c.now().Add(-retention)); err != nil && ctx.Err() == nil {
		log.Printf("limpar métricas antigas: %v", err)
	}
}
