package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// GetSetting lê uma configuração; ok=false quando a chave não existe.
func (d *DB) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := d.sql.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("ler configuração: %w", err)
	}
	return v, true, nil
}

// SetSetting grava a configuração, substituindo o valor existente.
func (d *DB) SetSetting(ctx context.Context, key, value string) error {
	if _, err := d.sql.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
		return fmt.Errorf("gravar configuração: %w", err)
	}
	return nil
}

// DeleteSetting remove a configuração; chave ausente não é erro.
func (d *DB) DeleteSetting(ctx context.Context, key string) error {
	if _, err := d.sql.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
		return fmt.Errorf("apagar configuração: %w", err)
	}
	return nil
}
