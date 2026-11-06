package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Session is a login session; only the hash of its token is stored.
type Session struct {
	ID         string
	TokenHash  string
	UserID     int64
	CSRFToken  string
	IP         string
	UserAgent  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

const sessionCols = `id, token_hash, user_id, csrf_token, ip, user_agent, created_at, last_seen_at, expires_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanSession(r rowScanner) (Session, error) {
	var s Session
	var c, l, e int64
	if err := r.Scan(&s.ID, &s.TokenHash, &s.UserID, &s.CSRFToken, &s.IP, &s.UserAgent, &c, &l, &e); err != nil {
		return Session{}, err
	}
	s.CreatedAt, s.LastSeenAt, s.ExpiresAt = time.Unix(c, 0), time.Unix(l, 0), time.Unix(e, 0)
	return s, nil
}

// CreateSession inserts a session.
func (d *DB) CreateSession(ctx context.Context, s Session) error {
	_, err := d.sql.ExecContext(ctx,
		`INSERT INTO sessions (`+sessionCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.TokenHash, s.UserID, s.CSRFToken, s.IP, s.UserAgent,
		s.CreatedAt.Unix(), s.LastSeenAt.Unix(), s.ExpiresAt.Unix())
	if err != nil {
		return fmt.Errorf("criar sessão: %w", err)
	}
	return nil
}

// SessionByTokenHash returns the session for a token hash, or ErrNotFound.
func (d *DB) SessionByTokenHash(ctx context.Context, hash string) (Session, error) {
	s, err := scanSession(d.sql.QueryRowContext(ctx,
		`SELECT `+sessionCols+` FROM sessions WHERE token_hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("buscar sessão: %w", err)
	}
	return s, nil
}

// TouchSession updates last_seen_at.
func (d *DB) TouchSession(ctx context.Context, id string, at time.Time) error {
	if _, err := d.sql.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at = ? WHERE id = ?`, at.Unix(), id); err != nil {
		return fmt.Errorf("atualizar sessão: %w", err)
	}
	return nil
}

// DeleteSession removes a session by id.
func (d *DB) DeleteSession(ctx context.Context, id string) error {
	if _, err := d.sql.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("remover sessão: %w", err)
	}
	return nil
}

// DeleteUserSession removes a session only if it belongs to userID.
func (d *DB) DeleteUserSession(ctx context.Context, userID int64, id string) (bool, error) {
	res, err := d.sql.ExecContext(ctx,
		`DELETE FROM sessions WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return false, fmt.Errorf("remover sessão: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("remover sessão: %w", err)
	}
	return n > 0, nil
}

// ListSessions returns a user's sessions, most recently seen first.
func (d *DB) ListSessions(ctx context.Context, userID int64) ([]Session, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT `+sessionCols+` FROM sessions WHERE user_id = ? ORDER BY last_seen_at DESC, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("listar sessões: %w", err)
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("listar sessões: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar sessões: %w", err)
	}
	return out, nil
}

// DeleteExpiredSessions removes sessions expired at now and returns how many.
func (d *DB) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := d.sql.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.Unix())
	if err != nil {
		return 0, fmt.Errorf("limpar sessões: %w", err)
	}
	return res.RowsAffected()
}
