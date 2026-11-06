// Package audit registra ações relevantes de segurança no log de auditoria.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/engviniciusbs/korego/internal/store"
)

// Entry descreve uma ação auditada. Metadata nunca deve conter senhas,
// códigos ou outros segredos.
type Entry struct {
	UserID   *int64
	Action   string
	Target   string
	IP       string
	Metadata map[string]any
}

// Logger grava entradas de auditoria no banco.
type Logger struct {
	DB  *store.DB
	Now func() time.Time
}

// Record grava e no log de auditoria.
func (l *Logger) Record(ctx context.Context, e Entry) error {
	meta := []byte("{}")
	if len(e.Metadata) > 0 {
		b, err := json.Marshal(e.Metadata)
		if err != nil {
			return fmt.Errorf("serializar metadados da auditoria: %w", err)
		}
		meta = b
	}
	now := time.Now()
	if l.Now != nil {
		now = l.Now()
	}
	return l.DB.InsertAudit(ctx, store.AuditRow{
		UserID:    e.UserID,
		Action:    e.Action,
		Target:    e.Target,
		IP:        e.IP,
		Metadata:  string(meta),
		CreatedAt: now,
	})
}
