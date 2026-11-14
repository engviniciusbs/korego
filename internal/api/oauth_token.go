package api

import (
	"errors"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/engviniciusbs/korego/internal/audit"
	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/oauth"
	"github.com/engviniciusbs/korego/internal/store"
)

// maxTokenBody limita o corpo do /oauth/token.
const maxTokenBody = 64 << 10

// tokenResponse é a resposta de sucesso do RFC 6749 §5.1.
func tokenResponse(w http.ResponseWriter, p auth.OAuthPair) {
	secs := int64(p.ExpiresIn.Seconds())
	if secs < 0 {
		secs = 0
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"access_token": p.Access, "token_type": "Bearer", "expires_in": secs, "refresh_token": p.Refresh})
}

// token trata POST /oauth/token (form-urlencoded, sem cookie nem CSRF).
func (s *oauthServer) token(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	now := s.now()
	key := "oauth-token:" + oauth.LimitKey(auth.ClientIP(r))
	if s.tokenLim.Blocked(key, now) {
		writeOAuthError(w, http.StatusTooManyRequests, "slow_down", "Muitas tentativas. Aguarde antes de tentar de novo.")
		return
	}
	// invalidGrant conta a falha no limitador e responde invalid_grant.
	invalidGrant := func(desc string) {
		s.tokenLim.Fail(key, s.now())
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", desc)
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/x-www-form-urlencoded" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Use Content-Type: application/x-www-form-urlencoded.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTokenBody)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "Corpo inválido ou grande demais.")
		return
	}
	// Só o corpo vale: parâmetros na query não entram.
	form := r.PostForm
	switch form.Get("grant_type") {
	case "authorization_code":
		s.codeGrant(w, r, form, invalidGrant)
	case "refresh_token":
		s.refreshGrant(w, r, form, invalidGrant)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "Use authorization_code ou refresh_token.")
	}
}

func (s *oauthServer) codeGrant(w http.ResponseWriter, r *http.Request, form url.Values, invalidGrant func(string)) {
	if res := form.Get("resource"); res != "" {
		if n, ok := oauth.NormalizeResource(res); !ok || n != originFrom(r.Context())+"/mcp" {
			writeOAuthError(w, http.StatusBadRequest, "invalid_target", "resource não corresponde a este servidor.")
			return
		}
	}
	hash := oauth.Hash(form.Get("code"))
	code, ok := s.codes.take(hash, s.now())
	if !ok {
		invalidGrant("Código inválido ou vencido.")
		return
	}
	if code.Used {
		// Reuso do código: a conexão que ele gerou é revogada (RFC 6749 §4.1.2).
		if code.TokenID > 0 {
			if err := s.d.Tokens.RevokeByID(r.Context(), code.TokenID); err != nil {
				log.Printf("oauth: revogar conexão do código reutilizado: %v", err)
			}
		}
		invalidGrant("Código já utilizado.")
		return
	}
	if form.Get("client_id") != code.ClientID || form.Get("redirect_uri") != code.RedirectURI ||
		!oauth.VerifyPKCE(form.Get("code_verifier"), code.Challenge) {
		invalidGrant("Código, cliente, retorno ou verificador não conferem.")
		return
	}
	// O cliente pode ter sido despejado entre a aprovação e a troca.
	if _, err := s.d.Auth.DB.OAuthClientByID(r.Context(), code.ClientID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			invalidGrant("Cliente não existe mais.")
			return
		}
		log.Printf("oauth: token: cliente: %v", err)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Erro interno.")
		return
	}
	pair, tok, err := s.d.Tokens.IssueOAuth(r.Context(), code.UserID, code.ClientName, code.ClientID, code.TTL)
	if err != nil {
		log.Printf("oauth: token: emitir: %v", err)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Erro interno.")
		return
	}
	if s.codes.setToken(hash, tok.ID) {
		// O código foi reapresentado durante a emissão: a conexão sai.
		if err := s.d.Tokens.RevokeByID(r.Context(), tok.ID); err != nil {
			log.Printf("oauth: revogar conexão do código reutilizado: %v", err)
		}
		invalidGrant("Código já utilizado.")
		return
	}
	tokenResponse(w, pair)
}

func (s *oauthServer) refreshGrant(w http.ResponseWriter, r *http.Request, form url.Values, invalidGrant func(string)) {
	refresh := strings.TrimSpace(form.Get("refresh_token"))
	pair, _, err := s.d.Tokens.Refresh(r.Context(), refresh, form.Get("client_id"))
	switch {
	case err == nil:
		tokenResponse(w, pair)
	case errors.Is(err, auth.ErrNoToken):
		invalidGrant("Refresh token inválido, vencido ou revogado.")
	case errors.Is(err, auth.ErrRefreshReuse):
		s.auditRefreshReuse(r, refresh)
		invalidGrant("Refresh token reutilizado: a conexão foi revogada.")
	default:
		log.Printf("oauth: token: renovar: %v", err)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "Erro interno.")
	}
}

// auditRefreshReuse registra o reuso; a linha da conexão sai pelo hash do
// refresh apresentado, pois Refresh não devolve o token nesse caso.
func (s *oauthServer) auditRefreshReuse(r *http.Request, refresh string) {
	tok, err := s.d.Auth.DB.APITokenByRefreshHash(r.Context(), oauth.Hash(refresh))
	if err != nil {
		log.Printf("oauth: auditar reuso do refresh: %v", err)
		return
	}
	ctx, cancel := detached(r, auditTimeout)
	defer cancel()
	uid := tok.UserID
	s.d.Auth.Record(ctx, audit.Entry{
		UserID: &uid, Action: "token.refresh_reuse", Target: tok.Name, IP: auth.ClientIP(r),
		Metadata: map[string]any{"tokenId": tok.ID, "client": tok.Name},
	})
}
