package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// base é um instante alinhado a 7 dias, múltiplo de qualquer passo usado.
const metricsBase = int64(1_700_000_000 - 1_700_000_000%86400)

func mm(ts int64, cpu float64) MetricMinute {
	return MetricMinute{TS: ts, CPUPercent: cpu, MemUsed: 100, MemTotal: 200, Load1: 1, NetRx: 10, NetTx: 20, DiskUsed: 5, DiskTotal: 50}
}

func TestMetricMinute_UpsertReplaces(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	require.NoError(t, db.UpsertMetricMinute(ctx, mm(metricsBase, 10)))
	require.NoError(t, db.UpsertMetricMinute(ctx, mm(metricsBase, 42)))

	got, err := db.MetricHistory(ctx, time.Unix(metricsBase, 0), time.Unix(metricsBase+60, 0), time.Minute)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, 42.0, got[0].CPUPercent)
	require.Equal(t, metricsBase, got[0].TS)
	require.Equal(t, uint64(200), got[0].MemTotal)
}

func TestMetricHistory_Buckets(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		require.NoError(t, db.UpsertMetricMinute(ctx, mm(metricsBase+int64(i)*60, float64(i))))
	}
	got, err := db.MetricHistory(ctx, time.Unix(metricsBase, 0), time.Unix(metricsBase+600, 0), 5*time.Minute)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, metricsBase, got[0].TS)
	require.Equal(t, 2.0, got[0].CPUPercent)
	require.Equal(t, metricsBase+300, got[1].TS)
	require.Equal(t, 7.0, got[1].CPUPercent)
}

func TestMetricHistory_KeepsGaps(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		require.NoError(t, db.UpsertMetricMinute(ctx, mm(metricsBase+int64(i)*60, 1)))
		require.NoError(t, db.UpsertMetricMinute(ctx, mm(metricsBase+int64(70+i)*60, 1)))
	}
	got, err := db.MetricHistory(ctx, time.Unix(metricsBase, 0), time.Unix(metricsBase+80*60, 0), time.Minute)
	require.NoError(t, err)
	require.Len(t, got, 20)
	for i := 1; i < len(got); i++ {
		require.Greater(t, got[i].TS, got[i-1].TS)
	}

	// passo grosso: nenhum balde pode cair dentro da lacuna (minutos 10..69)
	coarse, err := db.MetricHistory(ctx, time.Unix(metricsBase, 0), time.Unix(metricsBase+80*60, 0), 5*time.Minute)
	require.NoError(t, err)
	require.Len(t, coarse, 4)
	for _, p := range coarse {
		require.False(t, p.TS >= metricsBase+10*60 && p.TS < metricsBase+70*60, "balde na lacuna: %d", p.TS)
	}
}

func TestMetricHistory_UnalignedFromCapsPoints(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	tx, err := db.SQL().BeginTx(ctx, nil)
	require.NoError(t, err)
	for i := 0; i < 7*24*60+10; i++ {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO metrics_minute (ts, cpu_percent, mem_used, mem_total, load1, net_rx, net_tx, disk_used, disk_total) VALUES (?, 1, 1, 2, 1, 1, 1, 1, 2)`,
			metricsBase+int64(i)*60)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())

	from := time.Unix(metricsBase+7*60, 0)
	got, err := db.MetricHistory(ctx, from, from.Add(7*24*time.Hour), 30*time.Minute)
	require.NoError(t, err)
	require.LessOrEqual(t, len(got), 336)
	require.NotEmpty(t, got)
	require.Zero(t, got[0].TS%1800)
	require.GreaterOrEqual(t, got[0].TS, from.Unix())
}

func TestMetricHistory_7dMaxPoints(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	tx, err := db.SQL().BeginTx(ctx, nil)
	require.NoError(t, err)
	for i := 0; i < 7*24*60; i++ {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO metrics_minute (ts, cpu_percent, mem_used, mem_total, load1, net_rx, net_tx, disk_used, disk_total) VALUES (?, 1, 1, 2, 1, 1, 1, 1, 2)`,
			metricsBase+int64(i)*60)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())

	got, err := db.MetricHistory(ctx, time.Unix(metricsBase, 0), time.Unix(metricsBase+7*86400, 0), 30*time.Minute)
	require.NoError(t, err)
	require.Len(t, got, 336)
}

func TestDeleteMetricsBefore(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		require.NoError(t, db.UpsertMetricMinute(ctx, mm(metricsBase+int64(i)*60, 1)))
	}
	n, err := db.DeleteMetricsBefore(ctx, time.Unix(metricsBase+120, 0))
	require.NoError(t, err)
	require.Equal(t, int64(2), n)

	got, err := db.MetricHistory(ctx, time.Unix(metricsBase, 0), time.Unix(metricsBase+600, 0), time.Minute)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, metricsBase+120, got[0].TS)
}
