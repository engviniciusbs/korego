package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AuditRow is one audit_log entry; Metadata holds a JSON object.
type AuditRow struct {
	ID        int64
	UserID    *int64
	Action    string
	Target    string
	IP        string
	Metadata  string
	CreatedAt time.Time
}

// InsertAudit appends an entry to the audit log.
func (d *DB) InsertAudit(ctx context.Context, a AuditRow) error {
	meta := a.Metadata
	if meta == "" {
		meta = "{}"
	}
	if _, err := d.sql.ExecContext(ctx,
		`INSERT INTO audit_log (user_id, action, target, ip, metadata, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		a.UserID, a.Action, a.Target, a.IP, meta, a.CreatedAt.Unix()); err != nil {
		return fmt.Errorf("gravar auditoria: %w", err)
	}
	return nil
}

// ListAudit returns up to limit entries, newest first. A beforeID > 0 restricts
// the result to entries with id < beforeID (pagination).
func (d *DB) ListAudit(ctx context.Context, limit, beforeID int64) ([]AuditRow, error) {
	q := `SELECT id, user_id, action, target, ip, metadata, created_at FROM audit_log`
	args := []any{}
	if beforeID > 0 {
		q += ` WHERE id < ?`
		args = append(args, beforeID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := d.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listar auditoria: %w", err)
	}
	defer rows.Close()
	out := []AuditRow{}
	for rows.Next() {
		var a AuditRow
		var uid sql.NullInt64
		var created int64
		if err := rows.Scan(&a.ID, &uid, &a.Action, &a.Target, &a.IP, &a.Metadata, &created); err != nil {
			return nil, fmt.Errorf("listar auditoria: %w", err)
		}
		if uid.Valid {
			v := uid.Int64
			a.UserID = &v
		}
		a.CreatedAt = time.Unix(created, 0)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar auditoria: %w", err)
	}
	return out, nil
}
