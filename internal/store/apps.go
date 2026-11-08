package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// App é uma aplicação publicável.
type App struct {
	ID              int64
	ProjectID       int64
	Slug            string
	SourceType      string // github | git | image
	Repo            string
	Image           string
	BuildType       string // dockerfile | compose | railpack | image
	Branch          string
	DockerfilePath  string
	ComposePath     string
	ComposeService  string
	InternalPort    int
	HealthTimeoutS  int
	AutoDeploy      bool
	HostPort        int
	CurrentDeployID *int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

const appCols = `id, project_id, slug, source_type, repo, image, build_type, branch, dockerfile_path,
	compose_path, compose_service, internal_port, health_timeout_s, auto_deploy, host_port,
	current_deploy_id, created_at, updated_at`

func scanApp(r rowScanner) (App, error) {
	var a App
	var auto int
	var cur sql.NullInt64
	var created, updated int64
	if err := r.Scan(&a.ID, &a.ProjectID, &a.Slug, &a.SourceType, &a.Repo, &a.Image, &a.BuildType,
		&a.Branch, &a.DockerfilePath, &a.ComposePath, &a.ComposeService, &a.InternalPort,
		&a.HealthTimeoutS, &auto, &a.HostPort, &cur, &created, &updated); err != nil {
		return App{}, err
	}
	a.AutoDeploy = auto != 0
	if cur.Valid {
		v := cur.Int64
		a.CurrentDeployID = &v
	}
	a.CreatedAt, a.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return a, nil
}

// InsertApp grava a aplicação e preenche ID e carimbos. Slug repetido → ErrConflict.
func (d *DB) InsertApp(ctx context.Context, a *App) error {
	now := time.Now().Unix()
	res, err := d.sql.ExecContext(ctx,
		`INSERT INTO apps (project_id, slug, source_type, repo, image, build_type, branch, dockerfile_path,
		   compose_path, compose_service, internal_port, health_timeout_s, auto_deploy, host_port,
		   current_deploy_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ProjectID, a.Slug, a.SourceType, a.Repo, a.Image, a.BuildType, a.Branch, a.DockerfilePath,
		a.ComposePath, a.ComposeService, a.InternalPort, a.HealthTimeoutS, b2i(a.AutoDeploy), a.HostPort,
		nullInt(a.CurrentDeployID), now, now)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("aplicação %q: %w", a.Slug, ErrConflict)
		}
		return fmt.Errorf("inserir aplicação: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("inserir aplicação: %w", err)
	}
	a.ID = id
	a.CreatedAt, a.UpdatedAt = time.Unix(now, 0), time.Unix(now, 0)
	return nil
}

// UpdateApp atualiza os campos editáveis (não mexe em current_deploy_id; veja SetCurrentDeploy).
// Id inexistente → ErrNotFound; slug repetido → ErrConflict.
func (d *DB) UpdateApp(ctx context.Context, a *App) error {
	now := time.Now().Unix()
	res, err := d.sql.ExecContext(ctx,
		`UPDATE apps SET project_id = ?, slug = ?, source_type = ?, repo = ?, image = ?, build_type = ?,
		   branch = ?, dockerfile_path = ?, compose_path = ?, compose_service = ?, internal_port = ?,
		   health_timeout_s = ?, auto_deploy = ?, host_port = ?, updated_at = ? WHERE id = ?`,
		a.ProjectID, a.Slug, a.SourceType, a.Repo, a.Image, a.BuildType, a.Branch, a.DockerfilePath,
		a.ComposePath, a.ComposeService, a.InternalPort, a.HealthTimeoutS, b2i(a.AutoDeploy), a.HostPort,
		now, a.ID)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("aplicação %q: %w", a.Slug, ErrConflict)
		}
		return fmt.Errorf("atualizar aplicação: %w", err)
	}
	if err := needRows(res, "atualizar aplicação"); err != nil {
		return err
	}
	a.UpdatedAt = time.Unix(now, 0)
	return nil
}

// GetApp devolve a aplicação ou ErrNotFound.
func (d *DB) GetApp(ctx context.Context, id int64) (App, error) {
	return d.getApp(ctx, `WHERE id = ?`, id)
}

// GetAppBySlug devolve a aplicação pelo slug ou ErrNotFound.
func (d *DB) GetAppBySlug(ctx context.Context, slug string) (App, error) {
	return d.getApp(ctx, `WHERE slug = ?`, slug)
}

func (d *DB) getApp(ctx context.Context, where string, arg any) (App, error) {
	a, err := scanApp(d.sql.QueryRowContext(ctx, `SELECT `+appCols+` FROM apps `+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return App{}, ErrNotFound
	}
	if err != nil {
		return App{}, fmt.Errorf("ler aplicação: %w", err)
	}
	return a, nil
}

func (d *DB) queryApps(ctx context.Context, where string, args ...any) ([]App, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT `+appCols+` FROM apps `+where+` ORDER BY slug`, args...)
	if err != nil {
		return nil, fmt.Errorf("listar aplicações: %w", err)
	}
	defer rows.Close()
	out := []App{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, fmt.Errorf("listar aplicações: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar aplicações: %w", err)
	}
	return out, nil
}

// ListApps devolve as aplicações por slug, opcionalmente filtradas por projeto.
func (d *DB) ListApps(ctx context.Context, projectID *int64) ([]App, error) {
	if projectID == nil {
		return d.queryApps(ctx, "")
	}
	return d.queryApps(ctx, "WHERE project_id = ?", *projectID)
}

// AppsByRepo devolve as apps github com auto_deploy ligado para o repositório e branch.
func (d *DB) AppsByRepo(ctx context.Context, repo, branch string) ([]App, error) {
	return d.queryApps(ctx, `WHERE source_type = 'github' AND auto_deploy = 1 AND repo = ? AND branch = ?`, repo, branch)
}

// DeleteApp remove a aplicação (variáveis e deploys vão em cascata).
// Com domínios apontando → ErrConflict; id inexistente → ErrNotFound.
func (d *DB) DeleteApp(ctx context.Context, id int64) error {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("apagar aplicação: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM domains WHERE app_id = ?`, id).Scan(&n); err != nil {
		return fmt.Errorf("apagar aplicação: %w", err)
	}
	if n > 0 {
		return fmt.Errorf("aplicação possui domínios: %w", ErrConflict)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM apps WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("apagar aplicação: %w", err)
	}
	if err := needRows(res, "apagar aplicação"); err != nil {
		return err
	}
	return tx.Commit()
}

// UsedHostPorts devolve as portas do host em uso: as das apps e as dos deploys starting/running.
func (d *DB) UsedHostPorts(ctx context.Context) (map[int]bool, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT host_port FROM apps WHERE host_port > 0
		 UNION SELECT host_port FROM deploys WHERE host_port > 0 AND status IN ('starting', 'running')`)
	if err != nil {
		return nil, fmt.Errorf("portas em uso: %w", err)
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("portas em uso: %w", err)
		}
		out[p] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("portas em uso: %w", err)
	}
	return out, nil
}

// SetAppEnv aplica numa transação os upserts de set (nome → value_enc) e as remoções.
// Os nomes já chegam validados e os valores já cifrados.
func (d *DB) SetAppEnv(ctx context.Context, appID int64, set map[string]string, remove []string) error {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("gravar variáveis: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for name, enc := range set {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO app_env (app_id, name, value_enc) VALUES (?, ?, ?)
			 ON CONFLICT(app_id, name) DO UPDATE SET value_enc = excluded.value_enc`,
			appID, name, enc); err != nil {
			return fmt.Errorf("gravar variável: %w", err)
		}
	}
	for _, name := range remove {
		if _, err := tx.ExecContext(ctx, `DELETE FROM app_env WHERE app_id = ? AND name = ?`, appID, name); err != nil {
			return fmt.Errorf("remover variável: %w", err)
		}
	}
	return tx.Commit()
}

// AppEnvNames devolve os nomes das variáveis em ordem alfabética.
func (d *DB) AppEnvNames(ctx context.Context, appID int64) ([]string, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT name FROM app_env WHERE app_id = ? ORDER BY name`, appID)
	if err != nil {
		return nil, fmt.Errorf("listar variáveis: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("listar variáveis: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar variáveis: %w", err)
	}
	return out, nil
}

// AppEnvEncrypted devolve nome → value_enc de todas as variáveis da app.
func (d *DB) AppEnvEncrypted(ctx context.Context, appID int64) (map[string]string, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT name, value_enc FROM app_env WHERE app_id = ?`, appID)
	if err != nil {
		return nil, fmt.Errorf("ler variáveis: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var n, v string
		if err := rows.Scan(&n, &v); err != nil {
			return nil, fmt.Errorf("ler variáveis: %w", err)
		}
		out[n] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ler variáveis: %w", err)
	}
	return out, nil
}
