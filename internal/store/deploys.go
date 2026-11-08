package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Deploy é uma tentativa de publicar uma aplicação.
type Deploy struct {
	ID            int64
	AppID         int64
	Trigger       string
	Status        string // queued | building | starting | running | failed | superseded | cancelled
	Reason        string
	CommitSHA     string
	CommitMessage string
	Image         string
	HostPort      int
	ContainerID   string
	RollbackOf    *int64
	CreatedAt     time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
}

const deployCols = `id, app_id, trigger, status, reason, commit_sha, commit_message, image, host_port,
	container_id, rollback_of, created_at, started_at, finished_at`

func unixPtr(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := time.Unix(n.Int64, 0)
	return &t
}

func scanDeploy(r rowScanner) (Deploy, error) {
	var m Deploy
	var rb, started, finished sql.NullInt64
	var created int64
	if err := r.Scan(&m.ID, &m.AppID, &m.Trigger, &m.Status, &m.Reason, &m.CommitSHA, &m.CommitMessage,
		&m.Image, &m.HostPort, &m.ContainerID, &rb, &created, &started, &finished); err != nil {
		return Deploy{}, err
	}
	if rb.Valid {
		v := rb.Int64
		m.RollbackOf = &v
	}
	m.CreatedAt = time.Unix(created, 0)
	m.StartedAt, m.FinishedAt = unixPtr(started), unixPtr(finished)
	return m, nil
}

// InsertDeploy grava o deploy e preenche ID e CreatedAt.
func (d *DB) InsertDeploy(ctx context.Context, m *Deploy) error {
	now := time.Now().Unix()
	res, err := d.sql.ExecContext(ctx,
		`INSERT INTO deploys (app_id, trigger, status, reason, commit_sha, commit_message, image, host_port,
		   container_id, rollback_of, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.AppID, m.Trigger, m.Status, m.Reason, m.CommitSHA, m.CommitMessage, m.Image, m.HostPort,
		m.ContainerID, nullInt(m.RollbackOf), now)
	if err != nil {
		return fmt.Errorf("inserir deploy: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("inserir deploy: %w", err)
	}
	m.ID, m.CreatedAt = id, time.Unix(now, 0)
	return nil
}

// GetDeploy devolve o deploy ou ErrNotFound.
func (d *DB) GetDeploy(ctx context.Context, id int64) (Deploy, error) {
	m, err := scanDeploy(d.sql.QueryRowContext(ctx, `SELECT `+deployCols+` FROM deploys WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Deploy{}, ErrNotFound
	}
	if err != nil {
		return Deploy{}, fmt.Errorf("ler deploy: %w", err)
	}
	return m, nil
}

func queryDeploys(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, tail string, args ...any) ([]Deploy, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+deployCols+` FROM deploys `+tail, args...)
	if err != nil {
		return nil, fmt.Errorf("listar deploys: %w", err)
	}
	defer rows.Close()
	out := []Deploy{}
	for rows.Next() {
		m, err := scanDeploy(rows)
		if err != nil {
			return nil, fmt.Errorf("listar deploys: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar deploys: %w", err)
	}
	return out, nil
}

// ListDeploys devolve até limit deploys da app, o mais novo primeiro.
func (d *DB) ListDeploys(ctx context.Context, appID int64, limit int) ([]Deploy, error) {
	return queryDeploys(ctx, d.sql, `WHERE app_id = ? ORDER BY id DESC LIMIT ?`, appID, limit)
}

// QueuedDeploys devolve os deploys em fila, o mais antigo primeiro.
func (d *DB) QueuedDeploys(ctx context.Context) ([]Deploy, error) {
	return queryDeploys(ctx, d.sql, `WHERE status = 'queued' ORDER BY id`)
}

// UpdateDeployStatus muda o status e o motivo. Ao entrar em building define started_at (se ainda
// nulo); nos estados finais (running, failed, superseded, cancelled) define finished_at.
// Id inexistente → ErrNotFound.
func (d *DB) UpdateDeployStatus(ctx context.Context, id int64, status, reason string, now time.Time) error {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE deploys SET status = ?, reason = ?,
		   started_at = CASE WHEN ? = 'building' AND started_at IS NULL THEN ? ELSE started_at END,
		   finished_at = CASE WHEN ? IN ('running', 'failed', 'superseded', 'cancelled') THEN ? ELSE finished_at END
		 WHERE id = ?`,
		status, reason, status, now.Unix(), status, now.Unix(), id)
	if err != nil {
		return fmt.Errorf("atualizar deploy: %w", err)
	}
	return needRows(res, "atualizar deploy")
}

// SetDeployResult grava imagem, porta, container e dados do commit. Id inexistente → ErrNotFound.
func (d *DB) SetDeployResult(ctx context.Context, id int64, image string, hostPort int, containerID, sha, msg string) error {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE deploys SET image = ?, host_port = ?, container_id = ?, commit_sha = ?, commit_message = ? WHERE id = ?`,
		image, hostPort, containerID, sha, msg, id)
	if err != nil {
		return fmt.Errorf("gravar resultado do deploy: %w", err)
	}
	return needRows(res, "gravar resultado do deploy")
}

// SetCurrentDeploy aponta a app para o deploy corrente. App inexistente → ErrNotFound.
func (d *DB) SetCurrentDeploy(ctx context.Context, appID, deployID int64) error {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE apps SET current_deploy_id = ?, updated_at = ? WHERE id = ?`, deployID, time.Now().Unix(), appID)
	if err != nil {
		return fmt.Errorf("definir deploy corrente: %w", err)
	}
	return needRows(res, "definir deploy corrente")
}

// MarkInterrupted marca como failed ("interrompido") os deploys building/starting, numa transação,
// e devolve os afetados já atualizados.
func (d *DB) MarkInterrupted(ctx context.Context, now time.Time) ([]Deploy, error) {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("marcar interrompidos: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	const where = `WHERE status IN ('building', 'starting')`
	rows, err := queryDeploys(ctx, tx, where+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE deploys SET status = 'failed', reason = 'interrompido', finished_at = ? `+where, now.Unix()); err != nil {
			return nil, fmt.Errorf("marcar interrompidos: %w", err)
		}
		for i := range rows {
			rows[i].Status, rows[i].Reason = "failed", "interrompido"
			f := time.Unix(now.Unix(), 0)
			rows[i].FinishedAt = &f
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("marcar interrompidos: %w", err)
	}
	return rows, nil
}

// LatestImages devolve até n imagens distintas e não vazias dos deploys que chegaram a running ou
// superseded, a do deploy mais novo primeiro.
func (d *DB) LatestImages(ctx context.Context, appID int64, n int) ([]string, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT image FROM deploys WHERE app_id = ? AND image <> '' AND status IN ('running', 'superseded')
		 GROUP BY image ORDER BY MAX(id) DESC LIMIT ?`, appID, n)
	if err != nil {
		return nil, fmt.Errorf("listar imagens: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("listar imagens: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar imagens: %w", err)
	}
	return out, nil
}
