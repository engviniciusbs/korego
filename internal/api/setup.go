package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/store"
)

type userJSON struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

// toUserJSON exposes only safe fields (never the password hash or TOTP secret).
func toUserJSON(u store.User) userJSON {
	return userJSON{ID: u.ID, Email: u.Email, Role: u.Role, CreatedAt: u.CreatedAt}
}

type setupHandlers struct{ svc *auth.Service }

func (h setupHandlers) status(w http.ResponseWriter, r *http.Request) {
	need, err := h.svc.NeedsSetup(r.Context())
	if err != nil {
		internalError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"needsSetup": need})
}

func (h setupHandlers) begin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ch, err := h.svc.BeginSetup(r.Context(), req.Email, req.Password)
	switch {
	case err == nil:
		WriteJSON(w, http.StatusOK, map[string]string{
			"token": ch.Token, "secret": ch.Secret, "otpauthUrl": ch.OTPAuthURL,
		})
	case errors.Is(err, store.ErrSetupDone):
		setupDone(w)
	case errors.Is(err, auth.ErrWeakPassword):
		WriteError(w, http.StatusBadRequest, "WEAK_PASSWORD", "A senha deve ter pelo menos 12 caracteres.")
	case errors.Is(err, auth.ErrInvalidEmail):
		WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "E-mail inválido.")
	default:
		internalError(w, err)
	}
}

func (h setupHandlers) confirm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
		Code  string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ip, ua := clientInfo(r)
	res, codes, err := h.svc.ConfirmSetup(r.Context(), req.Token, req.Code, ip, ua)
	switch {
	case err == nil:
		setSessionCookie(w, r, res.Token)
		WriteJSON(w, http.StatusOK, map[string]any{
			"user": toUserJSON(res.User), "csrfToken": res.CSRFToken, "recoveryCodes": codes,
		})
	case errors.Is(err, store.ErrSetupDone):
		setupDone(w)
	case errors.Is(err, auth.ErrSetupTokenInvalid):
		WriteError(w, http.StatusBadRequest, "SETUP_TOKEN_INVALID", "Token de configuração inválido ou expirado.")
	case errors.Is(err, auth.ErrInvalidTOTP):
		WriteError(w, http.StatusBadRequest, "INVALID_TOTP", "Código do autenticador inválido.")
	case errors.Is(err, auth.ErrRateLimited):
		rateLimited(w)
	default:
		internalError(w, err)
	}
}

func setupDone(w http.ResponseWriter) {
	WriteError(w, http.StatusConflict, "SETUP_ALREADY_DONE", "A configuração inicial já foi concluída.")
}

func rateLimited(w http.ResponseWriter) {
	WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", "Muitas tentativas. Tente novamente mais tarde.")
}
