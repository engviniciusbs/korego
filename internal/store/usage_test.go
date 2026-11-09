package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUsage_UpsertAndAt(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	t0 := int64(1_700_000_040) // múltiplo de 60
	require.NoError(t, db.InsertUsageMinute(ctx, []UsageRow{
		{TS: t0, Key: "a", Kind: "app", Label: "A", CPUPercent: 10, MemBytes: 100},
		{TS: t0, Key: "b", Kind: "system", Label: "B", CPUPercent: 50, MemBytes: 10},
		{TS: t0, Key: "c", Kind: "docker", Label: "C", CPUPercent: 30, MemBytes: 1000},
		{TS: t0 + 60, Key: "a", Kind: "app", Label: "A", CPUPercent: 20, MemBytes: 200},
		{TS: t0 + 60, Key: "b", Kind: "system", Label: "B", CPUPercent: 70, MemBytes: 20},
		{TS: t0 + 60, Key: "c", Kind: "docker", Label: "C", CPUPercent: 10, MemBytes: 1001},
	}))
	// regravar o minuto substitui as linhas dele
	require.NoError(t, db.InsertUsageMinute(ctx, []UsageRow{
		{TS: t0, Key: "a", Kind: "app", Label: "A", CPUPercent: 0, MemBytes: 100},
		{TS: t0, Key: "b", Kind: "system", Label: "B", CPUPercent: 50, MemBytes: 10},
		{TS: t0, Key: "c", Kind: "docker", Label: "C", CPUPercent: 30, MemBytes: 1000},
	}))
	require.NoError(t, db.InsertUsageMinute(ctx, nil))

	from, to := time.Unix(t0, 0), time.Unix(t0+120, 0)
	got, err := db.UsageAt(ctx, from, to, time.Minute, "cpu", 10)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, []string{"b", "c", "a"}, []string{got[0].Key, got[1].Key, got[2].Key})
	require.InDelta(t, 60, got[0].CPUPercent, 0.001)
	require.InDelta(t, 20, got[1].CPUPercent, 0.001)
	require.InDelta(t, 10, got[2].CPUPercent, 0.001)
	require.Equal(t, "system", got[0].Kind)
	require.Equal(t, "B", got[0].Label)
	require.Equal(t, uint64(15), got[0].MemBytes)

	got, err = db.UsageAt(ctx, from, to, time.Minute, "mem", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"c", "a", "b"}, []string{got[0].Key, got[1].Key, got[2].Key})
	require.Equal(t, uint64(1001), got[0].MemBytes) // round(1000.5)

	got, err = db.UsageAt(ctx, from, to, time.Minute, "cpu", 2)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "b", got[0].Key)

	_, err = db.UsageAt(ctx, from, to, time.Minute, "disk", 10)
	require.ErrorIs(t, err, ErrInvalidMetric)
}

func TestUsage_AtEmptyWindow(t *testing.T) {
	db := OpenMemory(t)
	got, err := db.UsageAt(context.Background(), time.Unix(600, 0), time.Unix(660, 0), time.Minute, "cpu", 10)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestUsage_HistoryBucketsAndGaps(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	base := int64(1_700_000_100) // múltiplo de 300
	require.Zero(t, base%300)
	rows := []UsageRow{}
	// bucket 0: minutos 0..4 (cpu 10..50); bucket 1 vazio; bucket 2: minuto 0
	for i := int64(0); i < 5; i++ {
		rows = append(rows, UsageRow{TS: base + i*60, Key: "a", Kind: "app", Label: "A", CPUPercent: float64(10 * (i + 1)), MemBytes: 100})
	}
	rows = append(rows,
		UsageRow{TS: base + 600, Key: "a", Kind: "app", Label: "A", CPUPercent: 5, MemBytes: 7},
		UsageRow{TS: base, Key: "b", Kind: "app", Label: "B", CPUPercent: 99, MemBytes: 1},
	)
	require.NoError(t, db.InsertUsageMinute(ctx, rows))

	got, err := db.UsageHistory(ctx, "a", time.Unix(base-30, 0), time.Unix(base+900, 0), 5*time.Minute)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, base, got[0].TS)
	require.InDelta(t, 30, got[0].CPUPercent, 0.001)
	require.Equal(t, uint64(100), got[0].MemBytes)
	require.Equal(t, base+600, got[1].TS)

	got, err = db.UsageHistory(ctx, "zzz", time.Unix(base, 0), time.Unix(base+900, 0), 5*time.Minute)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestUsage_DeleteBefore(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	require.NoError(t, db.InsertUsageMinute(ctx, []UsageRow{
		{TS: 60, Key: "a", Kind: "app", Label: "A"},
		{TS: 120, Key: "a", Kind: "app", Label: "A"},
		{TS: 180, Key: "a", Kind: "app", Label: "A"},
	}))
	n, err := db.DeleteUsageBefore(ctx, time.Unix(180, 0))
	require.NoError(t, err)
	require.Equal(t, int64(2), n)
}

// Um dono que aparece em só 1 dos 5 minutos da janela entra com 1/5 do valor:
// a média divide pela quantidade de minutos com dados na janela (de todos os
// donos), não pelos minutos do próprio dono.
func TestUsage_AtDonoEsporadicoDividePelosMinutosDaJanela(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	base := int64(1_700_000_100) // múltiplo de 300
	rows := []UsageRow{}
	for i := int64(0); i < 5; i++ {
		rows = append(rows, UsageRow{TS: base + i*60, Key: "a", Kind: "app", Label: "A", CPUPercent: 10, MemBytes: 500})
	}
	rows = append(rows, UsageRow{TS: base + 120, Key: "s", Kind: "system", Label: "S", CPUPercent: 90, MemBytes: 1000})
	require.NoError(t, db.InsertUsageMinute(ctx, rows))

	got, err := db.UsageAt(ctx, time.Unix(base, 0), time.Unix(base+300, 0), 5*time.Minute, "cpu", 10)
	require.NoError(t, err)
	m := map[string]UsageRow{}
	for _, r := range got {
		m[r.Key] = r
	}
	require.Len(t, m, 2)
	require.InDelta(t, 18, m["s"].CPUPercent, 0.001) // 90 / 5
	require.Equal(t, uint64(200), m["s"].MemBytes)   // 1000 / 5
	require.InDelta(t, 10, m["a"].CPUPercent, 0.001)
	require.Equal(t, uint64(500), m["a"].MemBytes)
	require.Equal(t, []string{"s", "a"}, []string{got[0].Key, got[1].Key})

	got, err = db.UsageAt(ctx, time.Unix(base, 0), time.Unix(base+300, 0), 5*time.Minute, "mem", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "s"}, []string{got[0].Key, got[1].Key})
}

// Balde em que todos os donos estão em todos os minutos: igual à média simples.
func TestUsage_AtTodosPresentesFicaIgual(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	base := int64(1_700_000_100)
	rows := []UsageRow{}
	for i := int64(0); i < 5; i++ {
		rows = append(rows,
			UsageRow{TS: base + i*60, Key: "a", Kind: "app", Label: "A", CPUPercent: float64(10 * (i + 1)), MemBytes: 100},
			UsageRow{TS: base + i*60, Key: "b", Kind: "app", Label: "B", CPUPercent: 4, MemBytes: uint64(100 * (i + 1))},
		)
	}
	require.NoError(t, db.InsertUsageMinute(ctx, rows))
	got, err := db.UsageAt(ctx, time.Unix(base, 0), time.Unix(base+300, 0), 5*time.Minute, "cpu", 10)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "a", got[0].Key)
	require.InDelta(t, 30, got[0].CPUPercent, 0.001)
	require.Equal(t, uint64(100), got[0].MemBytes)
	require.InDelta(t, 4, got[1].CPUPercent, 0.001)
	require.Equal(t, uint64(300), got[1].MemBytes)

	h, err := db.UsageHistory(ctx, "a", time.Unix(base, 0), time.Unix(base+300, 0), 5*time.Minute)
	require.NoError(t, err)
	require.Len(t, h, 1)
	require.InDelta(t, 30, h[0].CPUPercent, 0.001)
}

// Histórico de um dono esporádico: cada balde divide pelos minutos com dados
// naquele balde, de qualquer dono.
func TestUsage_HistoryDonoEsporadico(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	base := int64(1_700_000_100) // múltiplo de 300
	rows := []UsageRow{}
	for i := int64(0); i < 10; i++ { // "a" em todos os minutos dos 2 baldes
		rows = append(rows, UsageRow{TS: base + i*60, Key: "a", Kind: "app", Label: "A", CPUPercent: 1, MemBytes: 1})
	}
	rows = append(rows,
		UsageRow{TS: base + 60, Key: "s", Kind: "app", Label: "S", CPUPercent: 90, MemBytes: 1000}, // balde 0: 1 de 5
		UsageRow{TS: base + 300, Key: "s", Kind: "app", Label: "S", CPUPercent: 50, MemBytes: 500}, // balde 1: 2 de 5
		UsageRow{TS: base + 540, Key: "s", Kind: "app", Label: "S", CPUPercent: 50, MemBytes: 1500},
	)
	require.NoError(t, db.InsertUsageMinute(ctx, rows))
	got, err := db.UsageHistory(ctx, "s", time.Unix(base, 0), time.Unix(base+600, 0), 5*time.Minute)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, base, got[0].TS)
	require.InDelta(t, 18, got[0].CPUPercent, 0.001)
	require.Equal(t, uint64(200), got[0].MemBytes)
	require.Equal(t, base+300, got[1].TS)
	require.InDelta(t, 20, got[1].CPUPercent, 0.001)
	require.Equal(t, uint64(400), got[1].MemBytes)
}

// Regravar um minuto (ex.: depois de reiniciar) substitui o minuto inteiro:
// donos que não estão na nova gravação somem, em vez de se misturar.
func TestUsage_InsertMinuteSubstituiOMinuto(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	t0 := int64(1_700_000_040)
	require.NoError(t, db.InsertUsageMinute(ctx, []UsageRow{
		{TS: t0, Key: "a", Kind: "app", Label: "A", CPUPercent: 10, MemBytes: 100},
		{TS: t0, Key: "rest", Kind: "other", Label: "Demais", CPUPercent: 5, MemBytes: 50},
		{TS: t0 + 60, Key: "b", Kind: "app", Label: "B", CPUPercent: 1, MemBytes: 1},
	}))
	require.NoError(t, db.InsertUsageMinute(ctx, []UsageRow{
		{TS: t0, Key: "a", Kind: "app", Label: "A2", CPUPercent: 20, MemBytes: 200},
	}))
	got, err := db.UsageAt(ctx, time.Unix(t0, 0), time.Unix(t0+60, 0), time.Minute, "cpu", 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "a", got[0].Key)
	require.Equal(t, "A2", got[0].Label)
	require.InDelta(t, 20, got[0].CPUPercent, 0.001)
	// o outro minuto não é tocado
	got, err = db.UsageAt(ctx, time.Unix(t0+60, 0), time.Unix(t0+120, 0), time.Minute, "cpu", 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "b", got[0].Key)
}

func TestUsage_TabelaSemRowid(t *testing.T) {
	db := OpenMemory(t)
	var sqlText string
	require.NoError(t, db.sql.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'usage_minute'`).Scan(&sqlText))
	require.Contains(t, sqlText, "WITHOUT ROWID")
	require.Contains(t, sqlText, "PRIMARY KEY (ts, owner_key)")
	var n int
	require.NoError(t, db.sql.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_usage_minute_owner'`).Scan(&n))
	require.Equal(t, 1, n)
}
