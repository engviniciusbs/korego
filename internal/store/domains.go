package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Header é um cabeçalho de resposta adicionado pelo proxy.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// DomainRoute é uma rota por caminho de um domínio. TargetType: app | address | static.
type DomainRoute struct {
	ID              int64
	DomainID        int64
	Position        int
	Path            string
	TargetType      string
	AppID           *int64
	Address         string
	MaxBodyBytes    int64
	NoBuffering     bool
	Root            string
	SPA             bool
	ImmutablePrefix string
}

// Domain é um hostname publicado pelo proxy.
//
// TargetType, AppID e Address espelham a rota "/" (preenchidos na leitura). Na
// gravação, sem Routes, eles viram uma única rota "/".
type Domain struct {
	ID             int64
	Hostname       string
	TargetType     string // app | address | static (da rota "/")
	AppID          *int64
	Address        string
	WWWRedirect    string // none | www_to_apex | apex_to_www
	ForceHTTPS     bool
	IsPanel        bool
	TLSMode        string // auto | custom
	CertID         *int64
	CloudflareOnly bool
	Headers        []Header
	Compression    bool
	Routes         []DomainRoute
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

const domainCols = `id, hostname, www_redirect, force_https, is_panel, tls_mode, cert_id,
	cloudflare_only, headers_json, compression, created_at, updated_at`

func scanDomain(r rowScanner) (Domain, error) {
	var d Domain
	var certID sql.NullInt64
	var force, panel, cf, comp int
	var headers string
	var created, updated int64
	if err := r.Scan(&d.ID, &d.Hostname, &d.WWWRedirect, &force, &panel, &d.TLSMode, &certID,
		&cf, &headers, &comp, &created, &updated); err != nil {
		return Domain{}, err
	}
	if certID.Valid {
		v := certID.Int64
		d.CertID = &v
	}
	d.Headers = []Header{}
	if err := json.Unmarshal([]byte(headers), &d.Headers); err != nil {
		return Domain{}, fmt.Errorf("cabeçalhos do domínio: %w", err)
	}
	d.ForceHTTPS, d.IsPanel = force != 0, panel != 0
	d.CloudflareOnly, d.Compression = cf != 0, comp != 0
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

const routeCols = `id, domain_id, position, path, target_type, app_id, address,
	max_body_bytes, no_buffering, root, spa, immutable_prefix`

func scanRoute(r rowScanner) (DomainRoute, error) {
	var rt DomainRoute
	var appID sql.NullInt64
	var nobuf, spa int
	if err := r.Scan(&rt.ID, &rt.DomainID, &rt.Position, &rt.Path, &rt.TargetType, &appID, &rt.Address,
		&rt.MaxBodyBytes, &nobuf, &rt.Root, &spa, &rt.ImmutablePrefix); err != nil {
		return DomainRoute{}, err
	}
	if appID.Valid {
		v := appID.Int64
		rt.AppID = &v
	}
	rt.NoBuffering, rt.SPA = nobuf != 0, spa != 0
	return rt, nil
}

// loadRoutes preenche Routes (ordenadas por position) e os campos legados a partir da rota "/".
func (d *DB) loadRoutes(ctx context.Context, doms []Domain) error {
	for i := range doms {
		rows, err := d.sql.QueryContext(ctx,
			`SELECT `+routeCols+` FROM domain_routes WHERE domain_id = ? ORDER BY position, id`, doms[i].ID)
		if err != nil {
			return fmt.Errorf("ler rotas: %w", err)
		}
		routes := []DomainRoute{}
		for rows.Next() {
			rt, err := scanRoute(rows)
			if err != nil {
				_ = rows.Close()
				return fmt.Errorf("ler rotas: %w", err)
			}
			routes = append(routes, rt)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return fmt.Errorf("ler rotas: %w", err)
		}
		doms[i].Routes = routes
		for _, rt := range routes {
			if rt.Path == "/" {
				doms[i].TargetType, doms[i].AppID, doms[i].Address = rt.TargetType, rt.AppID, rt.Address
				break
			}
		}
	}
	return nil
}

// effectiveRoutes devolve as rotas a gravar: as do domínio ou, se não houver,
// uma rota "/" montada a partir dos campos legados.
func effectiveRoutes(m *Domain) []DomainRoute {
	if len(m.Routes) > 0 {
		return m.Routes
	}
	return []DomainRoute{{Path: "/", TargetType: m.TargetType, AppID: m.AppID, Address: m.Address}}
}

// legacyTarget devolve (target_type, app_id, address) das colunas legadas, copiados da
// rota "/" quando é app ou address; ('address', NULL, ”) nos demais casos.
func legacyTarget(routes []DomainRoute) (string, any, string) {
	for _, r := range routes {
		if r.Path == "/" && (r.TargetType == "app" || r.TargetType == "address") {
			return r.TargetType, nullInt(r.AppID), r.Address
		}
	}
	return "address", nil, ""
}

func (m *Domain) normalize() (headersJSON string, tlsMode string, err error) {
	h := m.Headers
	if h == nil {
		h = []Header{}
	}
	b, err := json.Marshal(h)
	if err != nil {
		return "", "", err
	}
	tlsMode = m.TLSMode
	if tlsMode == "" {
		tlsMode = "auto"
	}
	return string(b), tlsMode, nil
}

func insertRoutes(ctx context.Context, tx *sql.Tx, domainID int64, routes []DomainRoute) error {
	for _, r := range routes {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO domain_routes (domain_id, position, path, target_type, app_id, address,
			   max_body_bytes, no_buffering, root, spa, immutable_prefix)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			domainID, r.Position, r.Path, r.TargetType, nullInt(r.AppID), r.Address,
			r.MaxBodyBytes, b2i(r.NoBuffering), r.Root, b2i(r.SPA), r.ImmutablePrefix); err != nil {
			if isUnique(err) {
				return fmt.Errorf("rota %q repetida: %w", r.Path, ErrConflict)
			}
			return fmt.Errorf("gravar rota: %w", err)
		}
	}
	return nil
}

// InsertDomain grava o domínio e suas rotas numa transação e preenche ID e carimbos.
// Hostname repetido → ErrConflict.
func (d *DB) InsertDomain(ctx context.Context, m *Domain) error {
	now := time.Now()
	headers, tlsMode, err := m.normalize()
	if err != nil {
		return fmt.Errorf("inserir domínio: %w", err)
	}
	routes := effectiveRoutes(m)
	lt, la, laddr := legacyTarget(routes)
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("inserir domínio: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO domains (hostname, target_type, app_id, address, www_redirect, force_https, is_panel,
		   tls_mode, cert_id, cloudflare_only, headers_json, compression, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Hostname, lt, la, laddr, m.WWWRedirect, b2i(m.ForceHTTPS), b2i(m.IsPanel),
		tlsMode, nullInt(m.CertID), b2i(m.CloudflareOnly), headers, b2i(m.Compression), now.Unix(), now.Unix())
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
	if err := insertRoutes(ctx, tx, id, routes); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("inserir domínio: %w", err)
	}
	m.ID, m.TLSMode = id, tlsMode
	m.CreatedAt, m.UpdatedAt = time.Unix(now.Unix(), 0), time.Unix(now.Unix(), 0)
	return nil
}

// CreateDomain é o nome do contrato de InsertDomain.
func (d *DB) CreateDomain(ctx context.Context, m *Domain) error { return d.InsertDomain(ctx, m) }

// UpdateDomain atualiza os campos editáveis e substitui as rotas numa transação.
// Id inexistente → ErrNotFound.
func (d *DB) UpdateDomain(ctx context.Context, m *Domain) error {
	now := time.Now().Unix()
	headers, tlsMode, err := m.normalize()
	if err != nil {
		return fmt.Errorf("atualizar domínio: %w", err)
	}
	routes := effectiveRoutes(m)
	lt, la, laddr := legacyTarget(routes)
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("atualizar domínio: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx,
		`UPDATE domains SET hostname = ?, target_type = ?, app_id = ?, address = ?, www_redirect = ?,
		   force_https = ?, is_panel = ?, tls_mode = ?, cert_id = ?, cloudflare_only = ?,
		   headers_json = ?, compression = ?, updated_at = ? WHERE id = ?`,
		m.Hostname, lt, la, laddr, m.WWWRedirect, b2i(m.ForceHTTPS), b2i(m.IsPanel),
		tlsMode, nullInt(m.CertID), b2i(m.CloudflareOnly), headers, b2i(m.Compression), now, m.ID)
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM domain_routes WHERE domain_id = ?`, m.ID); err != nil {
		return fmt.Errorf("atualizar domínio: %w", err)
	}
	if err := insertRoutes(ctx, tx, m.ID, routes); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("atualizar domínio: %w", err)
	}
	m.TLSMode = tlsMode
	m.UpdatedAt = time.Unix(now, 0)
	return nil
}

// DeleteDomain remove o domínio (as rotas saem em cascata). Id inexistente → ErrNotFound.
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

// GetDomain devolve o domínio com as rotas, ou ErrNotFound.
func (d *DB) GetDomain(ctx context.Context, id int64) (Domain, error) {
	m, err := scanDomain(d.sql.QueryRowContext(ctx,
		`SELECT `+domainCols+` FROM domains WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Domain{}, ErrNotFound
	}
	if err != nil {
		return Domain{}, fmt.Errorf("ler domínio: %w", err)
	}
	one := []Domain{m}
	if err := d.loadRoutes(ctx, one); err != nil {
		return Domain{}, err
	}
	return one[0], nil
}

// DomainByID é o nome do contrato de GetDomain.
func (d *DB) DomainByID(ctx context.Context, id int64) (Domain, error) { return d.GetDomain(ctx, id) }

func (d *DB) queryDomains(ctx context.Context, where string, args ...any) ([]Domain, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT `+domainCols+` FROM domains `+where+` ORDER BY hostname`, args...)
	if err != nil {
		return nil, fmt.Errorf("listar domínios: %w", err)
	}
	out := []Domain{}
	for rows.Next() {
		m, err := scanDomain(rows)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("listar domínios: %w", err)
		}
		out = append(out, m)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, fmt.Errorf("listar domínios: %w", err)
	}
	if err := d.loadRoutes(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListDomains devolve todos os domínios (com rotas) ordenados por hostname.
func (d *DB) ListDomains(ctx context.Context) ([]Domain, error) {
	return d.queryDomains(ctx, "")
}

// DomainsByApp devolve os domínios com alguma rota apontada para a app.
func (d *DB) DomainsByApp(ctx context.Context, appID int64) ([]Domain, error) {
	return d.queryDomains(ctx, "WHERE id IN (SELECT domain_id FROM domain_routes WHERE app_id = ?)", appID)
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
