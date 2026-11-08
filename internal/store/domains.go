package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Domain é um hostname publicado pelo proxy.
type Domain struct {
	ID          int64
	Hostname    string
	TargetType  string // app | address
	AppID       *int64
	Address     string
	WWWRedirect string // none | www_to_apex | apex_to_www
	ForceHTTPS  bool
	IsPanel     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const domainCols = `id, hostname, target_type, app_id, address, www_redirect, force_https, is_panel, created_at, updated_at`

func scanDomain(r rowScanner) (Domain, error) {
	var d Domain
	var appID sql.NullInt64
	var force, panel int
	var created, updated int64
	if err := r.Scan(&d.ID, &d.Hostname, &d.TargetType, &appID, &d.Address, &d.WWWRedirect,
		&force, &panel, &created, &updated); err != nil {
		return Domain{}, err
	}
	if appID.Valid {
		v := appID.Int64
		d.AppID = &v
	}
	d.ForceHTTPS, d.IsPanel = force != 0, panel != 0
	d.CreatedAt, d.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return d, nil
}

func nullInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// InsertDomain grava o domínio e preenche ID e carimbos. Hostname repetido → ErrConflict.
func (d *DB) InsertDomain(ctx context.Context, m *Domain) error {
	now := time.Now()
	res, err := d.sql.ExecContext(ctx,
		`INSERT INTO domains (hostname, target_type, app_id, address, www_redirect, force_https, is_panel, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Hostname, m.TargetType, nullInt(m.AppID), m.Address, m.WWWRedirect,
		b2i(m.ForceHTTPS), b2i(m.IsPanel), now.Unix(), now.Unix())
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("domínio %q: %w", m.Hostname, ErrConflict)
		}
		return fmt.Errorf("inserir domínio: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("inserir domínio: %w", err)
	}
	m.ID = id
	m.CreatedAt, m.UpdatedAt = time.Unix(now.Unix(), 0), time.Unix(now.Unix(), 0)
	return nil
}

// UpdateDomain atualiza todos os campos editáveis. Id inexistente → ErrNotFound.
func (d *DB) UpdateDomain(ctx context.Context, m *Domain) error {
	now := time.Now().Unix()
	res, err := d.sql.ExecContext(ctx,
		`UPDATE domains SET hostname = ?, target_type = ?, app_id = ?, address = ?, www_redirect = ?,
		   force_https = ?, is_panel = ?, updated_at = ? WHERE id = ?`,
		m.Hostname, m.TargetType, nullInt(m.AppID), m.Address, m.WWWRedirect,
		b2i(m.ForceHTTPS), b2i(m.IsPanel), now, m.ID)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("domínio %q: %w", m.Hostname, ErrConflict)
		}
		return fmt.Errorf("atualizar domínio: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("atualizar domínio: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	m.UpdatedAt = time.Unix(now, 0)
	return nil
}

// DeleteDomain remove o domínio. Id inexistente → ErrNotFound.
func (d *DB) DeleteDomain(ctx context.Context, id int64) error {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM domains WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("apagar domínio: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("apagar domínio: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetDomain devolve o domínio ou ErrNotFound.
func (d *DB) GetDomain(ctx context.Context, id int64) (Domain, error) {
	m, err := scanDomain(d.sql.QueryRowContext(ctx,
		`SELECT `+domainCols+` FROM domains WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Domain{}, ErrNotFound
	}
	if err != nil {
		return Domain{}, fmt.Errorf("ler domínio: %w", err)
	}
	return m, nil
}

func (d *DB) queryDomains(ctx context.Context, where string, args ...any) ([]Domain, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT `+domainCols+` FROM domains `+where+` ORDER BY hostname`, args...)
	if err != nil {
		return nil, fmt.Errorf("listar domínios: %w", err)
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		m, err := scanDomain(rows)
		if err != nil {
			return nil, fmt.Errorf("listar domínios: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar domínios: %w", err)
	}
	return out, nil
}

// ListDomains devolve todos os domínios ordenados por hostname.
func (d *DB) ListDomains(ctx context.Context) ([]Domain, error) {
	return d.queryDomains(ctx, "")
}

// DomainsByApp devolve os domínios apontados para a app.
func (d *DB) DomainsByApp(ctx context.Context, appID int64) ([]Domain, error) {
	return d.queryDomains(ctx, "WHERE app_id = ?", appID)
}

// PanelDomain devolve o domínio marcado como painel; ok=false se não houver.
func (d *DB) PanelDomain(ctx context.Context) (Domain, bool, error) {
	list, err := d.queryDomains(ctx, "WHERE is_panel = 1")
	if err != nil {
		return Domain{}, false, err
	}
	if len(list) == 0 {
		return Domain{}, false, nil
	}
	return list[0], true, nil
}
