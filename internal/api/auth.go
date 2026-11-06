package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/engviniciusbs/korego/internal/audit"
	"github.com/engviniciusbs/korego/internal/auth"
)

type authHandlers struct{ svc *auth.Service }

func (h authHandlers) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ip, ua := clientInfo(r)
	res, err := h.svc.Login(r.Context(), req.Email, req.Password, req.Code, ip, ua)
	switch {
	case err == nil:
		setSessionCookie(w, r, res.Token)
		WriteJSON(w, http.StatusOK, map[string]any{"user": toUserJSON(res.User), "csrfToken": res.CSRFToken})
	case errors.Is(err, auth.ErrInvalidCredentials):
		WriteError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "E-mail, senha ou código inválidos.")
	case errors.Is(err, auth.ErrRateLimited):
		rateLimited(w)
	case errors.Is(err, auth.ErrBusy):
		serverBusy(w)
	default:
		internalError(w, err)
	}
}

func (h authHandlers) logout(w http.ResponseWriter, r *http.Request) {
	sess, _ := CurrentSession(r.Context())
	if err := h.svc.Logout(r.Context(), sess.ID); err != nil {
		internalError(w, err)
		return
	}
	h.svc.Record(r.Context(), audit.Entry{UserID: &sess.UserID, Action: "auth.logout", Target: sess.ID, IP: auth.ClientIP(r)})
	clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (h authHandlers) me(w http.ResponseWriter, r *http.Request) {
	sess, _ := CurrentSession(r.Context())
	u, _ := CurrentUser(r.Context())
	WriteJSON(w, http.StatusOK, map[string]any{"user": toUserJSON(u), "csrfToken": sess.CSRFToken})
}

type sessionJSON struct {
	ID         string    `json:"id"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"userAgent"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Current    bool      `json:"current"`
}

func (h authHandlers) listSessions(w http.ResponseWriter, r *http.Request) {
	cur, _ := CurrentSession(r.Context())
	list, err := h.svc.DB.ListSessions(r.Context(), cur.UserID)
	if err != nil {
		internalError(w, err)
		return
	}
	items := make([]sessionJSON, 0, len(list))
	for _, s := range list {
		items = append(items, sessionJSON{
			ID: s.ID, IP: s.IP, UserAgent: s.UserAgent,
			CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, ExpiresAt: s.ExpiresAt,
			Current: s.ID == cur.ID,
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h authHandlers) revokeSession(w http.ResponseWriter, r *http.Request) {
	cur, _ := CurrentSession(r.Context())
	id := chi.URLParam(r, "id")
	ok, err := h.svc.DB.DeleteUserSession(r.Context(), cur.UserID, id)
	if err != nil {
		internalError(w, err)
		return
	}
	if !ok {
		WriteError(w, http.StatusNotFound, "NOT_FOUND", "Sessão não encontrada.")
		return
	}
	h.svc.Record(r.Context(), audit.Entry{UserID: &cur.UserID, Action: "auth.session_revoked", Target: id, IP: auth.ClientIP(r)})
	if id == cur.ID {
		clearSessionCookie(w, r)
	}
	w.WriteHeader(http.StatusNoContent)
}
