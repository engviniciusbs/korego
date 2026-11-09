package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// ErrInvalidMetric é devolvido quando a métrica de ordenação não é cpu nem mem.
var ErrInvalidMetric = errors.New("métrica inválida")

// UsageRow é o consumo de um dono (TS = início do minuto, ou do balde nas
// consultas agregadas, em unix).
type UsageRow struct {
	TS         int64
	Key        string
	Kind       string
	Label      string
	CPUPercent float64
	MemBytes   uint64
}

// InsertUsageMinute grava as linhas numa transação, substituindo as que já
// existem com o mesmo (ts, owner_key). Entrada vazia não faz nada.
func (d *DB) InsertUsageMinute(ctx context.Context, rows []UsageRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("gravar consumo: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO usage_minute (ts, owner_key, kind, label, cpu_percent, mem_bytes)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(ts, owner_key) DO UPDATE SET
		   kind = excluded.kind, label = excluded.label,
		   cpu_percent = excluded.cpu_percent, mem_bytes = excluded.mem_bytes`)
	if err != nil {
		return fmt.Errorf("gravar consumo: %w", err)
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.ExecContext(ctx, r.TS, r.Key, r.Kind, r.Label, r.CPUPercent, int64(r.MemBytes)); err != nil {
			return fmt.Errorf("gravar consumo: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("gravar consumo: %w", err)
	}
	return nil
}

// usageWindow calcula os limites [fromTS, to) como MetricHistory: from sobe
// ao próximo múltiplo de step.
func usageWindow(from, to time.Time, step time.Duration) (secs, fromTS, toTS int64) {
	secs = int64(step / time.Second)
	if secs < 1 {
		secs = 1
	}
	fromTS = from.Unix()
	if r := fromTS % secs; r != 0 {
		fromTS += secs - r
	}
	return secs, fromTS, to.Unix()
}

// UsageAt devolve a média de cada dono em [from, to) (from arredondado para
// cima ao múltiplo de step, como em MetricHistory), ordenada pela média da
// métrica ("cpu" ou "mem") em ordem decrescente, desempate por owner_key, com
// no máximo limit donos. Kind e label vêm da linha mais recente do dono na
// janela. TS fica zerado.
func (d *DB) UsageAt(ctx context.Context, from, to time.Time, step time.Duration, metric string, limit int) ([]UsageRow, error) {
	var order string
	switch metric {
	case "cpu":
		order = "AVG(u.cpu_percent)"
	case "mem":
		order = "AVG(u.mem_bytes)"
	default:
		return nil, ErrInvalidMetric
	}
	_, fromTS, toTS := usageWindow(from, to, step)
	rows, err := d.sql.QueryContext(ctx,
		`SELECT u.owner_key,
		        (SELECT kind  FROM usage_minute l WHERE l.owner_key = u.owner_key AND l.ts >= ?1 AND l.ts < ?2 ORDER BY l.ts DESC LIMIT 1),
		        (SELECT label FROM usage_minute l WHERE l.owner_key = u.owner_key AND l.ts >= ?1 AND l.ts < ?2 ORDER BY l.ts DESC LIMIT 1),
		        AVG(u.cpu_percent), AVG(u.mem_bytes)
		 FROM usage_minute u
		 WHERE u.ts >= ?1 AND u.ts < ?2
		 GROUP BY u.owner_key
		 ORDER BY `+order+` DESC, u.owner_key
		 LIMIT ?3`,
		fromTS, toTS, limit)
	if err != nil {
		return nil, fmt.Errorf("consultar consumo: %w", err)
	}
	defer rows.Close()
	out := []UsageRow{}
	for rows.Next() {
		var r UsageRow
		var mem float64
		if err := rows.Scan(&r.Key, &r.Kind, &r.Label, &r.CPUPercent, &mem); err != nil {
			return nil, fmt.Errorf("consultar consumo: %w", err)
		}
		r.MemBytes = uint64(math.Round(mem))
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("consultar consumo: %w", err)
	}
	return out, nil
}

// UsageHistory devolve a média de um dono por balde de step (ts - ts % step)
// em [from, to), em ordem crescente e só com os baldes que têm dados.
func (d *DB) UsageHistory(ctx context.Context, key string, from, to time.Time, step time.Duration) ([]UsageRow, error) {
	secs, fromTS, toTS := usageWindow(from, to, step)
	rows, err := d.sql.QueryContext(ctx,
		`SELECT ts - ts % ?1 AS bucket, AVG(cpu_percent), AVG(mem_bytes)
		 FROM usage_minute
		 WHERE owner_key = ?2 AND ts >= ?3 AND ts < ?4
		 GROUP BY bucket
		 ORDER BY bucket`,
		secs, key, fromTS, toTS)
	if err != nil {
		return nil, fmt.Errorf("consultar histórico de consumo: %w", err)
	}
	defer rows.Close()
	out := []UsageRow{}
	for rows.Next() {
		r := UsageRow{Key: key}
		var mem float64
		if err := rows.Scan(&r.TS, &r.CPUPercent, &mem); err != nil {
			return nil, fmt.Errorf("consultar histórico de consumo: %w", err)
		}
		r.MemBytes = uint64(math.Round(mem))
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("consultar histórico de consumo: %w", err)
	}
	return out, nil
}

// DeleteUsageBefore apaga as linhas com ts < t e devolve quantas foram removidas.
func (d *DB) DeleteUsageBefore(ctx context.Context, t time.Time) (int64, error) {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM usage_minute WHERE ts < ?`, t.Unix())
	if err != nil {
		return 0, fmt.Errorf("limpar consumo: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("limpar consumo: %w", err)
	}
	return n, nil
}
