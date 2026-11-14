package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// APIToken is a long-lived API credential; only the hash of its secret is stored.
type APIToken struct {
	ID, UserID int64
	Name       string
	TokenHash  string
	Prefix     string // first 10 chars of the secret ("kg_" + 7)
	CreatedAt  time.Time
	LastUsedAt *time.Time
	LastUsedIP string
	ExpiresAt  *time.Time // nil = never
	RevokedAt  *time.Time
}

const apiTokenCols = `id, user_id, name, token_hash, prefix, created_at, last_used_at, last_used_ip, expires_at, revoked_at`

// nullUnix converts an optional time to a nullable unix-seconds SQL value.
func nullUnix(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Unix()
}

func scanAPIToken(r rowScanner) (APIToken, error) {
	var t APIToken
	var c int64
	var used, exp, rev sql.NullInt64
	if err := r.Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, &t.Prefix, &c, &used, &t.LastUsedIP, &exp, &rev); err != nil {
		return APIToken{}, err
	}
	t.CreatedAt = time.Unix(c, 0)
	t.LastUsedAt, t.ExpiresAt, t.RevokedAt = unixPtr(used), unixPtr(exp), unixPtr(rev)
	return t, nil
}

// CreateAPIToken inserts a token and returns its id. It returns ErrConflict
// if the user already has an active (non-revoked) token with the same name.
func (d *DB) CreateAPIToken(ctx context.Context, t APIToken) (int64, error) {
	res, err := d.sql.ExecContext(ctx,
		`INSERT INTO api_tokens (user_id, name, token_hash, prefix, created_at, last_used_at, last_used_ip, expires_at, revoked_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.UserID, t.Name, t.TokenHash, t.Prefix, t.CreatedAt.Unix(),
		nullUnix(t.LastUsedAt), t.LastUsedIP, nullUnix(t.ExpiresAt), nullUnix(t.RevokedAt))
	if isUnique(err) {
		return 0, ErrConflict
	}
	if err != nil {
		return 0, fmt.Errorf("criar token de API: %w", err)
	}
	return res.LastInsertId()
}

// APITokenByHash returns the token for a secret hash, or ErrNotFound.
func (d *DB) APITokenByHash(ctx context.Context, hash string) (APIToken, error) {
	t, err := scanAPIToken(d.sql.QueryRowContext(ctx,
		`SELECT `+apiTokenCols+` FROM api_tokens WHERE token_hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, ErrNotFound
	}
	if err != nil {
		return APIToken{}, fmt.Errorf("buscar token de API: %w", err)
	}
	return t, nil
}

// ListAPITokens returns a user's non-revoked tokens, newest first.
func (d *DB) ListAPITokens(ctx context.Context, userID int64) ([]APIToken, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT `+apiTokenCols+` FROM api_tokens WHERE user_id = ? AND revoked_at IS NULL ORDER BY created_at DESC, id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("listar tokens de API: %w", err)
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, fmt.Errorf("listar tokens de API: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar tokens de API: %w", err)
	}
	return out, nil
}

// RevokeAPIToken marks a user's token as revoked at the given time and returns
// it. It returns ErrNotFound if the token is missing, belongs to another user
// or is already revoked.
func (d *DB) RevokeAPIToken(ctx context.Context, userID, id int64, at time.Time) (APIToken, error) {
	t, err := scanAPIToken(d.sql.QueryRowContext(ctx,
		`UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL RETURNING `+apiTokenCols,
		at.Unix(), id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, ErrNotFound
	}
	if err != nil {
		return APIToken{}, fmt.Errorf("revogar token de API: %w", err)
	}
	return t, nil
}

// TouchAPIToken records the last use of a token.
func (d *DB) TouchAPIToken(ctx context.Context, id int64, at time.Time, ip string) error {
	if _, err := d.sql.ExecContext(ctx,
		`UPDATE api_tokens SET last_used_at = ?, last_used_ip = ? WHERE id = ?`, at.Unix(), ip, id); err != nil {
		return fmt.Errorf("atualizar token de API: %w", err)
	}
	return nil
}
