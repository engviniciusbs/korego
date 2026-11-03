package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// User is a KoreGO account.
type User struct {
	ID            int64
	Email         string
	PasswordHash  string
	Role          string
	TOTPSecretEnc string
	TOTPLastStep  int64
	CreatedAt     time.Time
}

func normEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// CreateAdminIfNone inserts the first admin and its recovery code hashes in an
// immediate write transaction. It returns ErrSetupDone if any user exists.
func (d *DB) CreateAdminIfNone(ctx context.Context, u User, recoveryHashes []string) (int64, error) {
	conn, err := d.sql.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("obter conexão: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return 0, fmt.Errorf("iniciar transação: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// Fresh context so rollback works even if ctx was canceled.
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var n int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		return 0, fmt.Errorf("contar usuários: %w", err)
	}
	if n > 0 {
		return 0, ErrSetupDone
	}
	role := u.Role
	if role == "" {
		role = "admin"
	}
	res, err := conn.ExecContext(ctx,
		`INSERT INTO users (email, password_hash, role, totp_secret_enc, totp_last_step, created_at)
		 VALUES (?, ?, ?, ?, 0, ?)`,
		normEmail(u.Email), u.PasswordHash, role, u.TOTPSecretEnc, time.Now().Unix())
	if err != nil {
		return 0, fmt.Errorf("inserir usuário: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, h := range recoveryHashes {
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO recovery_codes (user_id, code_hash) VALUES (?, ?)", id, h); err != nil {
			return 0, fmt.Errorf("inserir código de recuperação: %w", err)
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return 0, fmt.Errorf("confirmar transação: %w", err)
	}
	committed = true
	return id, nil
}

// CountUsers returns the number of users.
func (d *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := d.sql.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n)
	return n, err
}

const userCols = "id, email, password_hash, role, totp_secret_enc, totp_last_step, created_at"

func scanUser(row *sql.Row) (User, error) {
	var u User
	var created int64
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.TOTPSecretEnc, &u.TOTPLastStep, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	u.CreatedAt = time.Unix(created, 0)
	return u, nil
}

// UserByEmail looks a user up by (case-insensitive) email.
func (d *DB) UserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(d.sql.QueryRowContext(ctx,
		"SELECT "+userCols+" FROM users WHERE email = ?", normEmail(email)))
}

// UserByID looks a user up by id.
func (d *DB) UserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(d.sql.QueryRowContext(ctx,
		"SELECT "+userCols+" FROM users WHERE id = ?", id))
}

// AdvanceTOTPStep records step if it is greater than the stored one.
func (d *DB) AdvanceTOTPStep(ctx context.Context, userID, step int64) (bool, error) {
	res, err := d.sql.ExecContext(ctx,
		"UPDATE users SET totp_last_step = ? WHERE id = ? AND totp_last_step < ?", step, userID, step)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// UseRecoveryCode marks an unused recovery code as used.
func (d *DB) UseRecoveryCode(ctx context.Context, userID int64, hash string) (bool, error) {
	res, err := d.sql.ExecContext(ctx,
		`UPDATE recovery_codes SET used_at = ?
		 WHERE id = (SELECT id FROM recovery_codes WHERE user_id = ? AND code_hash = ? AND used_at IS NULL LIMIT 1)`,
		time.Now().Unix(), userID, hash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
