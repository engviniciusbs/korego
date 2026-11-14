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

// TokenRef identifica o token de API que originou uma requisição.
type TokenRef struct {
	ID   int64
	Name string
}

type tokenCtxKey struct{}

// WithToken marca ctx como originado por um token de API.
func WithToken(ctx context.Context, ref TokenRef) context.Context {
	return context.WithValue(ctx, tokenCtxKey{}, ref)
}

// TokenFrom retorna o token de API associado a ctx, se houver.
func TokenFrom(ctx context.Context) (TokenRef, bool) {
	ref, ok := ctx.Value(tokenCtxKey{}).(TokenRef)
	return ref, ok
}

// Logger grava entradas de auditoria no banco.
type Logger struct {
	DB  *store.DB
	Now func() time.Time
}

// Record grava e no log de auditoria.
func (l *Logger) Record(ctx context.Context, e Entry) error {
	meta := []byte("{}")
	md := e.Metadata
	if ref, ok := TokenFrom(ctx); ok {
		md = make(map[string]any, len(e.Metadata)+3)
		for k, v := range e.Metadata {
			md[k] = v
		}
		md["via"] = "token"
		md["token"] = ref.Name
		md["tokenId"] = ref.ID
	}
	if len(md) > 0 {
		b, err := json.Marshal(md)
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
