package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// GitHubApp é o GitHub App registrado (linha única, id=1). A chave privada e o
// segredo do webhook ficam cifrados.
type GitHubApp struct {
	AppID            int64
	Slug             string
	Owner            string
	HTMLURL          string
	PrivateKeyEnc    string
	WebhookSecretEnc string
	CreatedAt        time.Time
}

// SaveGitHubApp grava (ou substitui) o GitHub App.
func (d *DB) SaveGitHubApp(ctx context.Context, a *GitHubApp) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO github_app (id, app_id, slug, owner, html_url, private_key_enc, webhook_secret_enc, created_at)
		 VALUES (1, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET app_id = excluded.app_id, slug = excluded.slug, owner = excluded.owner,
		   html_url = excluded.html_url, private_key_enc = excluded.private_key_enc,
		   webhook_secret_enc = excluded.webhook_secret_enc, created_at = excluded.created_at`,
		a.AppID, a.Slug, a.Owner, a.HTMLURL, a.PrivateKeyEnc, a.WebhookSecretEnc, a.CreatedAt.Unix())
	if err != nil {
		return fmt.Errorf("gravar github app: %w", err)
	}
	return nil
}

// GetGitHubApp lê o GitHub App; ErrNotFound quando não conectado.
func (d *DB) GetGitHubApp(ctx context.Context) (GitHubApp, error) {
	var a GitHubApp
	var created int64
	err := d.sql.QueryRowContext(ctx,
		`SELECT app_id, slug, owner, html_url, private_key_enc, webhook_secret_enc, created_at FROM github_app WHERE id = 1`).
		Scan(&a.AppID, &a.Slug, &a.Owner, &a.HTMLURL, &a.PrivateKeyEnc, &a.WebhookSecretEnc, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return GitHubApp{}, ErrNotFound
	}
	if err != nil {
		return GitHubApp{}, fmt.Errorf("ler github app: %w", err)
	}
	a.CreatedAt = time.Unix(created, 0)
	return a, nil
}

// DeleteGitHubApp remove o GitHub App; ausente não é erro.
func (d *DB) DeleteGitHubApp(ctx context.Context) error {
	if _, err := d.sql.ExecContext(ctx, `DELETE FROM github_app WHERE id = 1`); err != nil {
		return fmt.Errorf("apagar github app: %w", err)
	}
	return nil
}
