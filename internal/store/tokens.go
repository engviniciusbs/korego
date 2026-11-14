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
	Prefix     string // first 10 chars of the secret ("kg_" + 7, ou "kgo_" + 6 no OAuth)
	CreatedAt  time.Time
	LastUsedAt *time.Time
	LastUsedIP string
	ExpiresAt  *time.Time // nil = never
	RevokedAt  *time.Time

	// Conexões OAuth (Kind == "oauth"); em tokens manuais ficam vazios.
	Kind            string // "manual" (padrão) ou "oauth"
	ClientID        string
	AccessExpiresAt *time.Time
	RefreshHash     string
	PrevRefreshHash string // refresh anterior, aceito na janela de rotação
	PrevAccessHash  string
	RotatedAt       *time.Time
}

// TokenRotation são os novos valores de uma rotação de token OAuth.
type TokenRotation struct {
	AccessHash, RefreshHash, Prefix string
	AccessExpiresAt, At             time.Time
}

const apiTokenCols = `id, user_id, name, token_hash, prefix, created_at, last_used_at, last_used_ip, expires_at, revoked_at,
	kind, client_id, access_expires_at, refresh_hash, prev_refresh_hash, prev_access_hash, rotated_at`

// nullStr grava string vazia como NULL.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

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
	var used, exp, rev, accExp, rot sql.NullInt64
	var refresh, prevRefresh, prevAccess sql.NullString
	if err := r.Scan(&t.ID, &t.UserID, &t.Name, &t.TokenHash, &t.Prefix, &c, &used, &t.LastUsedIP, &exp, &rev,
		&t.Kind, &t.ClientID, &accExp, &refresh, &prevRefresh, &prevAccess, &rot); err != nil {
		return APIToken{}, err
	}
	t.CreatedAt = time.Unix(c, 0)
	t.LastUsedAt, t.ExpiresAt, t.RevokedAt = unixPtr(used), unixPtr(exp), unixPtr(rev)
	t.AccessExpiresAt, t.RotatedAt = unixPtr(accExp), unixPtr(rot)
	t.RefreshHash, t.PrevRefreshHash, t.PrevAccessHash = refresh.String, prevRefresh.String, prevAccess.String
	return t, nil
}

// CreateAPIToken inserts a token and returns its id. It returns ErrConflict
// if the user already has an active (non-revoked) token with the same name.
func (d *DB) CreateAPIToken(ctx context.Context, t APIToken) (int64, error) {
	kind := t.Kind
	if kind == "" {
		kind = "manual"
	}
	res, err := d.sql.ExecContext(ctx,
		`INSERT INTO api_tokens (user_id, name, token_hash, prefix, created_at, last_used_at, last_used_ip, expires_at, revoked_at,
			kind, client_id, access_expires_at, refresh_hash, prev_refresh_hash, prev_access_hash, rotated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.UserID, t.Name, t.TokenHash, t.Prefix, t.CreatedAt.Unix(),
		nullUnix(t.LastUsedAt), t.LastUsedIP, nullUnix(t.ExpiresAt), nullUnix(t.RevokedAt),
		kind, t.ClientID, nullUnix(t.AccessExpiresAt), nullStr(t.RefreshHash),
		nullStr(t.PrevRefreshHash), nullStr(t.PrevAccessHash), nullUnix(t.RotatedAt))
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

// apiTokenWhere busca um token por uma condição fixa (nunca vinda de entrada externa).
func (d *DB) apiTokenWhere(ctx context.Context, where string, args ...any) (APIToken, error) {
	t, err := scanAPIToken(d.sql.QueryRowContext(ctx,
		`SELECT `+apiTokenCols+` FROM api_tokens WHERE `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, ErrNotFound
	}
	if err != nil {
		return APIToken{}, fmt.Errorf("buscar token de API: %w", err)
	}
	return t, nil
}

// APITokenByRefreshHash devolve o token cujo refresh atual ou anterior casa
// com o hash, ou ErrNotFound.
func (d *DB) APITokenByRefreshHash(ctx context.Context, hash string) (APIToken, error) {
	return d.apiTokenWhere(ctx, `refresh_hash = ? OR prev_refresh_hash = ?`, hash, hash)
}

// APITokenByID devolve o token pelo id, sem conferir o dono, ou ErrNotFound.
func (d *DB) APITokenByID(ctx context.Context, id int64) (APIToken, error) {
	return d.apiTokenWhere(ctx, `id = ?`, id)
}

// RevokeOtherOAuthTokens revoga as conexões OAuth vivas do usuário com o mesmo
// client_id, exceto keepID: autorizar o cliente de novo substitui a conexão
// antiga. Devolve quantas foram revogadas.
func (d *DB) RevokeOtherOAuthTokens(ctx context.Context, userID int64, clientID string, keepID int64, at time.Time) (int64, error) {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE api_tokens SET revoked_at = ?
		WHERE user_id = ? AND kind = 'oauth' AND client_id = ? AND id <> ? AND revoked_at IS NULL`,
		at.Unix(), userID, clientID, keepID)
	if err != nil {
		return 0, fmt.Errorf("revogar conexões anteriores: %w", err)
	}
	return res.RowsAffected()
}

// APITokenByPrevAccessHash devolve o token cujo acesso anterior casa com o
// hash, ou ErrNotFound.
func (d *DB) APITokenByPrevAccessHash(ctx context.Context, hash string) (APIToken, error) {
	return d.apiTokenWhere(ctx, `prev_access_hash = ?`, hash)
}

// RotateAPIToken troca acesso e refresh de um token OAuth. É um CAS em
// refresh_hash = expectRefreshHash, só em linha não revogada; sem linha
// afetada devolve ErrConflict.
func (d *DB) RotateAPIToken(ctx context.Context, id int64, expectRefreshHash string, r TokenRotation) error {
	q := `UPDATE api_tokens SET token_hash = ?, refresh_hash = ?, prefix = ?, access_expires_at = ?,
		prev_access_hash = token_hash, prev_refresh_hash = refresh_hash, rotated_at = ?
		WHERE id = ? AND refresh_hash = ? AND revoked_at IS NULL`
	args := []any{r.AccessHash, r.RefreshHash, r.Prefix, r.AccessExpiresAt.Unix(), r.At.Unix(), id, expectRefreshHash}
	res, err := d.sql.ExecContext(ctx, q, args...)
	if isUnique(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("rotacionar token de API: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rotacionar token de API: %w", err)
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

// RevokeAPITokenByID revoga um token sem conferir o dono (revogação por reuso).
func (d *DB) RevokeAPITokenByID(ctx context.Context, id int64, at time.Time) error {
	if _, err := d.sql.ExecContext(ctx,
		`UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, at.Unix(), id); err != nil {
		return fmt.Errorf("revogar token de API: %w", err)
	}
	return nil
}
