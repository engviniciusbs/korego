package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Project agrupa aplicações.
type Project struct {
	ID          int64
	Name        string
	Description string
	CreatedAt   time.Time
}

const projectCols = `id, name, description, created_at`

func scanProject(r rowScanner) (Project, error) {
	var p Project
	var created int64
	if err := r.Scan(&p.ID, &p.Name, &p.Description, &created); err != nil {
		return Project{}, err
	}
	p.CreatedAt = time.Unix(created, 0)
	return p, nil
}

// InsertProject grava o projeto e preenche ID e CreatedAt. Nome repetido → ErrConflict.
func (d *DB) InsertProject(ctx context.Context, p *Project) error {
	now := time.Now().Unix()
	res, err := d.sql.ExecContext(ctx,
		`INSERT INTO projects (name, description, created_at) VALUES (?, ?, ?)`, p.Name, p.Description, now)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("projeto %q: %w", p.Name, ErrConflict)
		}
		return fmt.Errorf("inserir projeto: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("inserir projeto: %w", err)
	}
	p.ID, p.CreatedAt = id, time.Unix(now, 0)
	return nil
}

// UpdateProject atualiza nome e descrição. Id inexistente → ErrNotFound; nome repetido → ErrConflict.
func (d *DB) UpdateProject(ctx context.Context, p *Project) error {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE projects SET name = ?, description = ? WHERE id = ?`, p.Name, p.Description, p.ID)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("projeto %q: %w", p.Name, ErrConflict)
		}
		return fmt.Errorf("atualizar projeto: %w", err)
	}
	return needRows(res, "atualizar projeto")
}

// needRows devolve ErrNotFound quando o comando não afetou nenhuma linha.
func needRows(res sql.Result, op string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetProject devolve o projeto ou ErrNotFound.
func (d *DB) GetProject(ctx context.Context, id int64) (Project, error) {
	p, err := scanProject(d.sql.QueryRowContext(ctx, `SELECT `+projectCols+` FROM projects WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("ler projeto: %w", err)
	}
	return p, nil
}

// ListProjects devolve os projetos ordenados por nome.
func (d *DB) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT `+projectCols+` FROM projects ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listar projetos: %w", err)
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("listar projetos: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar projetos: %w", err)
	}
	return out, nil
}

// DeleteProject remove o projeto. Com aplicações → ErrConflict; id inexistente → ErrNotFound.
func (d *DB) DeleteProject(ctx context.Context, id int64) error {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("apagar projeto: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM apps WHERE project_id = ?`, id).Scan(&n); err != nil {
		return fmt.Errorf("apagar projeto: %w", err)
	}
	if n > 0 {
		return fmt.Errorf("projeto possui aplicações: %w", ErrConflict)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("apagar projeto: %w", err)
	}
	if err := needRows(res, "apagar projeto"); err != nil {
		return err
	}
	return tx.Commit()
}
