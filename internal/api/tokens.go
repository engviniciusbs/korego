package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/engviniciusbs/korego/internal/audit"
	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/store"
)

type tokenHandlers struct {
	svc    *auth.Service
	tokens *auth.Tokens
}

type tokenDTO struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	LastUsedIP string     `json:"lastUsedIp"`
	ExpiresAt  *time.Time `json:"expiresAt"`
}

func toTokenDTO(t store.APIToken) tokenDTO {
	return tokenDTO{ID: t.ID, Name: t.Name, Prefix: t.Prefix, CreatedAt: t.CreatedAt,
		LastUsedAt: t.LastUsedAt, LastUsedIP: t.LastUsedIP, ExpiresAt: t.ExpiresAt}
}

func (h tokenHandlers) audit(r *http.Request, userID int64, action string, meta map[string]any) {
	ctx, cancel := detached(r, auditTimeout)
	defer cancel()
	h.svc.Record(ctx, audit.Entry{UserID: &userID, Action: action, IP: auth.ClientIP(r), Metadata: meta})
}

// list serve GET /api/tokens.
func (h tokenHandlers) list(w http.ResponseWriter, r *http.Request) {
	sess, _ := CurrentSession(r.Context())
	toks, err := h.tokens.DB.ListAPITokens(r.Context(), sess.UserID)
	if err != nil {
		internalError(w, err)
		return
	}
	items := make([]tokenDTO, 0, len(toks))
	for _, t := range toks {
		items = append(items, toTokenDTO(t))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// create serve POST /api/tokens: transporte, corpo, código e só então emite.
func (h tokenHandlers) create(w http.ResponseWriter, r *http.Request) {
	sess, _ := CurrentSession(r.Context())
	if !tokenTransportOK(r) {
		WriteError(w, http.StatusForbidden, "TOKEN_REQUIRES_HTTPS", msgTokenHTTPS)
		return
	}
	var in struct {
		Name          string `json:"name"`
		ExpiresInDays int    `json:"expiresInDays"`
		Code          string `json:"code"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.ExpiresInDays != 0 && in.ExpiresInDays != 30 && in.ExpiresInDays != 90 {
		WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "A validade deve ser nunca, 30 ou 90 dias.")
		return
	}
	ip := auth.ClientIP(r)
	rec := func(meta map[string]any) { h.audit(r, sess.UserID, "api_token.create", meta) }
	if !verifyCode(h.svc, w, r, sess.UserID, ip, in.Code, rec) {
		return
	}
	ttl := time.Duration(in.ExpiresInDays) * 24 * time.Hour
	secret, tok, err := h.tokens.Issue(r.Context(), sess.UserID, in.Name, ttl)
	switch {
	case errors.Is(err, auth.ErrInvalidTokenName):
		WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "O nome do token deve ter de 1 a 64 caracteres.")
		return
	case errors.Is(err, store.ErrConflict):
		WriteError(w, http.StatusConflict, "TOKEN_NAME_TAKEN", "Já existe um token ativo com esse nome.")
		return
	case err != nil:
		internalError(w, err)
		return
	}
	h.audit(r, sess.UserID, "api_token.create",
		map[string]any{"ok": true, "name": tok.Name, "tokenId": tok.ID, "expiresInDays": in.ExpiresInDays})
	WriteJSON(w, http.StatusCreated, map[string]any{"token": secret, "item": toTokenDTO(tok)})
}

// revoke serve DELETE /api/tokens/{id}.
func (h tokenHandlers) revoke(w http.ResponseWriter, r *http.Request) {
	sess, _ := CurrentSession(r.Context())
	id, ok := pathID(w, r, "id", "VALIDATION_ERROR", "ID de token inválido.")
	if !ok {
		return
	}
	tok, err := h.tokens.DB.RevokeAPIToken(r.Context(), sess.UserID, id, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		WriteError(w, http.StatusNotFound, "TOKEN_NOT_FOUND", "Token não encontrado.")
		return
	}
	if err != nil {
		internalError(w, err)
		return
	}
	h.audit(r, sess.UserID, "api_token.revoke", map[string]any{"ok": true, "name": tok.Name, "tokenId": tok.ID})
	w.WriteHeader(http.StatusNoContent)
}
