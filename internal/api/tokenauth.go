package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/engviniciusbs/korego/internal/audit"
	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/store"
)

const (
	// codeHeader leva o código 2FA das requisições por token às rotas protegidas.
	codeHeader = "X-Korego-Code"

	msgCodeRequired = "Confirme com o código do autenticador ou um código de recuperação."
	msgInvalidCode  = "Código inválido."
	msgRateLimited  = "Muitas tentativas. Tente novamente mais tarde."
	msgTokenHTTPS   = "Tokens de API só funcionam via HTTPS. Configure o domínio do painel em Configurações → Painel."
)

// identityHeaders são os únicos cabeçalhos guardados da requisição original:
// os que definem o IP do cliente, o esquema e o user agent.
var identityHeaders = []string{"X-Forwarded-For", "X-Forwarded-Proto", "X-Real-IP", "CF-Connecting-IP", "User-Agent"}

// forwardingHeaders indicam que a requisição passou por um proxy.
var forwardingHeaders = []string{"X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host", "Forwarded"}

// tokenIdentity é quem fez uma requisição por token, guardada para reaplicar
// nas requisições internas do despacho.
type tokenIdentity struct {
	Token      store.APIToken
	User       store.User
	RemoteAddr string
	// Header tem só identityHeaders.
	Header http.Header
}

// tokenTransportOK diz se um token pode trafegar nesta requisição: por HTTPS
// (direto ou pelo proxy local) ou de loopback sem nenhum cabeçalho de proxy.
func tokenTransportOK(r *http.Request) bool {
	if requestIsHTTPS(r) {
		return true
	}
	if !auth.FromLoopback(r) {
		return false
	}
	for _, h := range forwardingHeaders {
		if _, ok := r.Header[http.CanonicalHeaderKey(h)]; ok {
			return false
		}
	}
	return true
}

// tokenLimitKey é a chave do limitador de tokens, separada da do login.
func tokenLimitKey(r *http.Request) string { return "token:" + auth.ClientIP(r) }

// bearerToken lê "Authorization: Bearer <token>" (esquema sem distinção de
// maiúsculas, espaços extras aceitos). Nunca lê o token da query.
func bearerToken(r *http.Request) (string, bool) {
	f := strings.Fields(r.Header.Get("Authorization"))
	if len(f) != 2 || !strings.EqualFold(f[0], "bearer") {
		return "", false
	}
	return f[1], true
}

// withTokenIdentity põe em ctx a sessão sintética, o usuário, o token da
// auditoria e a identidade.
func withTokenIdentity(ctx context.Context, id tokenIdentity) context.Context {
	ctx = context.WithValue(ctx, sessionKey, store.Session{
		ID: "token:" + strconv.FormatInt(id.Token.ID, 10), UserID: id.Token.UserID,
	})
	ctx = context.WithValue(ctx, userKey, id.User)
	ctx = audit.WithToken(ctx, audit.TokenRef{ID: id.Token.ID, Name: id.Token.Name})
	return context.WithValue(ctx, tokenIdentityKey, id)
}

func copyIdentityHeaders(h http.Header) http.Header {
	out := http.Header{}
	for _, k := range identityHeaders {
		if v := h.Values(k); len(v) > 0 {
			out[http.CanonicalHeaderKey(k)] = append([]string(nil), v...)
		}
	}
	return out
}

// RequireToken validates the Bearer token and puts in ctx: sessionKey =
// store.Session{ID: "token:<id>", UserID: tok.UserID}, userKey, audit.WithToken,
// and tokenIdentityKey. Order: transport (403 TOKEN_REQUIRES_HTTPS), limiter
// (429 RATE_LIMITED), token (401 INVALID_TOKEN, counted as a failure).
// A valid token does not reset the limiter, so it cannot clear failed codes.
func RequireToken(tokens *auth.Tokens, db *store.DB, lim *auth.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !tokenTransportOK(r) {
				WriteError(w, http.StatusForbidden, "TOKEN_REQUIRES_HTTPS", msgTokenHTTPS)
				return
			}
			key, now := tokenLimitKey(r), clock(tokens.Now)
			if lim != nil && lim.Blocked(key, now) {
				WriteError(w, http.StatusTooManyRequests, "RATE_LIMITED", msgRateLimited)
				return
			}
			tok, u, err := resolveToken(r, tokens, db)
			if errors.Is(err, auth.ErrNoToken) {
				if lim != nil {
					lim.Fail(key, now)
				}
				WriteError(w, http.StatusUnauthorized, "INVALID_TOKEN", "Token de API inválido, expirado ou revogado.")
				return
			}
			if err != nil {
				internalError(w, err)
				return
			}
			id := tokenIdentity{Token: tok, User: u, RemoteAddr: r.RemoteAddr, Header: copyIdentityHeaders(r.Header)}
			next.ServeHTTP(w, r.WithContext(withTokenIdentity(r.Context(), id)))
		})
	}
}

// resolveToken valida o Bearer e busca o dono; ausente, inválido ou sem dono
// dão auth.ErrNoToken.
func resolveToken(r *http.Request, tokens *auth.Tokens, db *store.DB) (store.APIToken, store.User, error) {
	secret, ok := bearerToken(r)
	if !ok {
		return store.APIToken{}, store.User{}, auth.ErrNoToken
	}
	tok, err := tokens.Validate(r.Context(), secret, auth.ClientIP(r))
	if err != nil {
		return store.APIToken{}, store.User{}, err
	}
	u, err := db.UserByID(r.Context(), tok.UserID)
	if errors.Is(err, store.ErrNotFound) {
		return store.APIToken{}, store.User{}, auth.ErrNoToken
	}
	return tok, u, err
}

func clock(now func() time.Time) time.Time {
	if now != nil {
		return now()
	}
	return time.Now()
}

// codeConfirmed diz se requireCodeForToken já conferiu o código desta requisição.
func codeConfirmed(ctx context.Context) bool {
	ok, _ := ctx.Value(codeConfirmedKey).(bool)
	return ok
}

// requireCodeForToken: no-op for cookie requests; for token requests needs
// header X-Korego-Code, verified with auth.Service.VerifyCode and the token
// limiter (key "token:"+ip); on success marks ctx so confirmCode returns true
// without reading the body. It answers before the handler runs: 429
// RATE_LIMITED, 403 CODE_REQUIRED or 403 INVALID_CODE, each audited as
// api_token.code. Only a wrong code counts toward the limiter.
func requireCodeForToken(svc *auth.Service, lim *auth.Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, isToken := audit.TokenFrom(r.Context()); !isToken {
				next.ServeHTTP(w, r)
				return
			}
			sess, ok := CurrentSession(r.Context())
			if !ok {
				unauthorized(w)
				return
			}
			refuse := func(status int, code, msg, stage string) {
				record(svc, r, "api_token.code", r.Method+" "+r.URL.Path, map[string]any{"ok": false, "stage": stage})
				WriteError(w, status, code, msg)
			}
			key, now := tokenLimitKey(r), clock(svc.Now)
			if lim != nil && lim.Blocked(key, now) {
				refuse(http.StatusTooManyRequests, "RATE_LIMITED", msgRateLimited, "rate_limited")
				return
			}
			code := strings.TrimSpace(r.Header.Get(codeHeader))
			if code == "" {
				refuse(http.StatusForbidden, "CODE_REQUIRED", msgCodeRequired, "code_required")
				return
			}
			ctx, cancel := detached(r, auditTimeout)
			valid, err := svc.VerifyCode(ctx, sess.UserID, code)
			cancel()
			if err != nil {
				internalError(w, err)
				return
			}
			if !valid {
				if lim != nil {
					lim.Fail(key, now)
				}
				refuse(http.StatusForbidden, "INVALID_CODE", msgInvalidCode, "invalid_code")
				return
			}
			if lim != nil {
				lim.Reset(key)
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), codeConfirmedKey, true)))
		})
	}
}
