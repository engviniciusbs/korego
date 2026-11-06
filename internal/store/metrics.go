package store

import (
	"context"
	"fmt"
	"math"
	"time"
)

// MetricMinute é a média das amostras de um minuto (TS = início do minuto, unix).
// Nas consultas por balde, TS é o início do balde e os valores são médias.
type MetricMinute struct {
	TS         int64
	CPUPercent float64
	MemUsed    uint64
	MemTotal   uint64
	Load1      float64
	NetRx      float64 // bytes/s
	NetTx      float64 // bytes/s
	DiskUsed   uint64
	DiskTotal  uint64
}

// UpsertMetricMinute grava o minuto, substituindo a linha existente com o mesmo ts.
func (d *DB) UpsertMetricMinute(ctx context.Context, m MetricMinute) error {
	if _, err := d.sql.ExecContext(ctx,
		`INSERT INTO metrics_minute (ts, cpu_percent, mem_used, mem_total, load1, net_rx, net_tx, disk_used, disk_total)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(ts) DO UPDATE SET
		   cpu_percent = excluded.cpu_percent,
		   mem_used = excluded.mem_used, mem_total = excluded.mem_total,
		   load1 = excluded.load1,
		   net_rx = excluded.net_rx, net_tx = excluded.net_tx,
		   disk_used = excluded.disk_used, disk_total = excluded.disk_total`,
		m.TS, m.CPUPercent, int64(m.MemUsed), int64(m.MemTotal), m.Load1,
		m.NetRx, m.NetTx, int64(m.DiskUsed), int64(m.DiskTotal)); err != nil {
		return fmt.Errorf("gravar métrica: %w", err)
	}
	return nil
}

// MetricHistory devolve a média por balde de step (ts - ts % step) no intervalo
// [from, to), em ordem crescente e só com os baldes que têm dados.
// from é arredondado para cima ao próximo múltiplo de step (mantido se já
// alinhado): o balde inicial parcial é descartado, então uma janela de N*step
// nunca devolve mais de N pontos. to continua exclusivo.
func (d *DB) MetricHistory(ctx context.Context, from, to time.Time, step time.Duration) ([]MetricMinute, error) {
	secs := int64(step / time.Second)
	if secs < 1 {
		secs = 1
	}
	fromTS := from.Unix()
	if r := fromTS % secs; r != 0 {
		fromTS += secs - r
	}
	rows, err := d.sql.QueryContext(ctx,
		`SELECT ts - ts % ?1 AS bucket,
		        AVG(cpu_percent), AVG(mem_used), AVG(mem_total), AVG(load1),
		        AVG(net_rx), AVG(net_tx), AVG(disk_used), AVG(disk_total)
		 FROM metrics_minute
		 WHERE ts >= ?2 AND ts < ?3
		 GROUP BY bucket
		 ORDER BY bucket`,
		secs, fromTS, to.Unix())
	if err != nil {
		return nil, fmt.Errorf("consultar métricas: %w", err)
	}
	defer rows.Close()
	out := []MetricMinute{}
	for rows.Next() {
		var m MetricMinute
		var memUsed, memTotal, diskUsed, diskTotal float64
		if err := rows.Scan(&m.TS, &m.CPUPercent, &memUsed, &memTotal, &m.Load1,
			&m.NetRx, &m.NetTx, &diskUsed, &diskTotal); err != nil {
			return nil, fmt.Errorf("consultar métricas: %w", err)
		}
		m.MemUsed, m.MemTotal = uint64(math.Round(memUsed)), uint64(math.Round(memTotal))
		m.DiskUsed, m.DiskTotal = uint64(math.Round(diskUsed)), uint64(math.Round(diskTotal))
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("consultar métricas: %w", err)
	}
	return out, nil
}

// DeleteMetricsBefore apaga os minutos com ts < t e devolve quantos foram removidos.
func (d *DB) DeleteMetricsBefore(ctx context.Context, t time.Time) (int64, error) {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM metrics_minute WHERE ts < ?`, t.Unix())
	if err != nil {
		return 0, fmt.Errorf("limpar métricas: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("limpar métricas: %w", err)
	}
	return n, nil
}
