package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// OAuthClient é um cliente registrado dinamicamente (agente MCP).
type OAuthClient struct {
	ID, Name     string
	RedirectURIs []string
	CreatedAt    time.Time
	AuthorizedAt *time.Time // nil = nunca autorizado
}

// CreateOAuthClient grava um cliente novo. Devolve ErrConflict se o id já existe.
func (d *DB) CreateOAuthClient(ctx context.Context, c OAuthClient) error {
	uris, err := json.Marshal(c.RedirectURIs)
	if err != nil {
		return fmt.Errorf("criar cliente OAuth: %w", err)
	}
	_, err = d.sql.ExecContext(ctx,
		`INSERT INTO oauth_clients (id, name, redirect_uris, created_at, authorized_at) VALUES (?, ?, ?, ?, ?)`,
		c.ID, c.Name, string(uris), c.CreatedAt.Unix(), nullUnix(c.AuthorizedAt))
	if isUnique(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("criar cliente OAuth: %w", err)
	}
	return nil
}

// OAuthClientByID devolve o cliente, ou ErrNotFound.
func (d *DB) OAuthClientByID(ctx context.Context, id string) (OAuthClient, error) {
	var c OAuthClient
	var uris string
	var created int64
	var auth sql.NullInt64
	err := d.sql.QueryRowContext(ctx,
		`SELECT id, name, redirect_uris, created_at, authorized_at FROM oauth_clients WHERE id = ?`, id).
		Scan(&c.ID, &c.Name, &uris, &created, &auth)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthClient{}, ErrNotFound
	}
	if err != nil {
		return OAuthClient{}, fmt.Errorf("buscar cliente OAuth: %w", err)
	}
	if err := json.Unmarshal([]byte(uris), &c.RedirectURIs); err != nil {
		return OAuthClient{}, fmt.Errorf("buscar cliente OAuth: %w", err)
	}
	c.CreatedAt = time.Unix(created, 0)
	c.AuthorizedAt = unixPtr(auth)
	return c, nil
}

// MarkOAuthClientAuthorized registra a primeira autorização do cliente
// (as seguintes mantêm a data original).
func (d *DB) MarkOAuthClientAuthorized(ctx context.Context, id string, at time.Time) error {
	if _, err := d.sql.ExecContext(ctx,
		`UPDATE oauth_clients SET authorized_at = ? WHERE id = ? AND authorized_at IS NULL`, at.Unix(), id); err != nil {
		return fmt.Errorf("autorizar cliente OAuth: %w", err)
	}
	return nil
}

// CountUnauthorizedOAuthClients conta os clientes nunca autorizados.
func (d *DB) CountUnauthorizedOAuthClients(ctx context.Context) (int, error) {
	var n int
	if err := d.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM oauth_clients WHERE authorized_at IS NULL`).Scan(&n); err != nil {
		return 0, fmt.Errorf("contar clientes OAuth: %w", err)
	}
	return n, nil
}

// DeleteOldestUnauthorizedOAuthClient remove o cliente nunca autorizado mais antigo.
func (d *DB) DeleteOldestUnauthorizedOAuthClient(ctx context.Context) error {
	if _, err := d.sql.ExecContext(ctx,
		`DELETE FROM oauth_clients WHERE id = (
			SELECT id FROM oauth_clients WHERE authorized_at IS NULL ORDER BY created_at ASC, rowid ASC LIMIT 1)`); err != nil {
		return fmt.Errorf("remover cliente OAuth: %w", err)
	}
	return nil
}

// PruneOAuthClients apaga os clientes nunca autorizados criados antes de
// createdBefore e devolve quantos saíram.
func (d *DB) PruneOAuthClients(ctx context.Context, createdBefore time.Time) (int64, error) {
	res, err := d.sql.ExecContext(ctx,
		`DELETE FROM oauth_clients WHERE authorized_at IS NULL AND created_at < ?`, createdBefore.Unix())
	if err != nil {
		return 0, fmt.Errorf("limpar clientes OAuth: %w", err)
	}
	return res.RowsAffected()
}
