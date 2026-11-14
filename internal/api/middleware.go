package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/store"
)

const (
	sessionCookieName = "korego_session"
	maxBodyBytes      = 64 << 10
	maxUserAgentLen   = 512
)

type ctxKey int

const (
	sessionKey ctxKey = iota
	userKey
	tokenIdentityKey
	codeConfirmedKey
)

// CurrentSession returns the session placed in ctx by RequireSession.
func CurrentSession(ctx context.Context) (store.Session, bool) {
	s, ok := ctx.Value(sessionKey).(store.Session)
	return s, ok
}

// CurrentUser returns the user placed in ctx by RequireSession.
func CurrentUser(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(userKey).(store.User)
	return u, ok
}

// RequireSession resolves the korego_session cookie into a live session and
// its user; otherwise it answers 401 UNAUTHORIZED.
func RequireSession(svc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(sessionCookieName)
			if err != nil || c.Value == "" {
				unauthorized(w)
				return
			}
			sess, err := svc.Sessions.Validate(r.Context(), c.Value)
			if errors.Is(err, auth.ErrNoSession) {
				unauthorized(w)
				return
			}
			if err != nil {
				internalError(w, err)
				return
			}
			u, err := svc.DB.UserByID(r.Context(), sess.UserID)
			if errors.Is(err, store.ErrNotFound) {
				unauthorized(w)
				return
			}
			if err != nil {
				internalError(w, err)
				return
			}
			ctx := context.WithValue(r.Context(), sessionKey, sess)
			ctx = context.WithValue(ctx, userKey, u)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireCSRF demands, for state-changing methods, an X-CSRF-Token header equal
// to the session's token (constant-time compare); otherwise 403 CSRF_INVALID.
// It must run after RequireSession.
func RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		sess, ok := CurrentSession(r.Context())
		got := r.Header.Get("X-CSRF-Token")
		if !ok || got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRFToken)) != 1 {
			WriteError(w, http.StatusForbidden, "CSRF_INVALID", "Token CSRF ausente ou inválido.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func unauthorized(w http.ResponseWriter) {
	WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Sessão inválida ou expirada.")
}

func internalError(w http.ResponseWriter, err error) {
	log.Printf("erro interno: %v", err)
	WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Erro interno do servidor.")
}

// decodeJSON reads a size-limited JSON body into v, rejecting unknown fields
// and trailing data. On failure it writes 400 VALIDATION_ERROR.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil && dec.Decode(&struct{}{}) != io.EOF {
		err = errors.New("dados extras após o JSON")
	}
	if err != nil {
		WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Corpo da requisição inválido.")
		return false
	}
	return true
}

// requestIsHTTPS reports whether the client reached us over HTTPS, directly or
// through a trusted local reverse proxy.
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if !auth.FromLoopback(r) {
		return false
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	proto = proto[strings.LastIndex(proto, ",")+1:]
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(auth.SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func clientInfo(r *http.Request) (ip, ua string) {
	ua = r.UserAgent()
	if len(ua) > maxUserAgentLen {
		ua = ua[:maxUserAgentLen]
	}
	return auth.ClientIP(r), ua
}
