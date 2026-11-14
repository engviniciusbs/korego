package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/oauth"
	"github.com/engviniciusbs/korego/internal/store"
)

// maxRegisterBody limita o corpo do registro dinâmico.
const maxRegisterBody = 64 << 10

// register trata POST /oauth/register (RFC 7591), tolerante: só o
// redirect_uri pode causar recusa.
func (s *oauthServer) register(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	if !s.regLim.Attempt("oauth-reg:"+oauth.LimitKey(auth.ClientIP(r)), now) {
		writeOAuthError(w, http.StatusTooManyRequests, "slow_down", "Muitos registros. Tente novamente mais tarde.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRegisterBody))
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "Corpo inválido ou grande demais.")
		return
	}
	reg, err := oauth.SanitizeRegistration(json.RawMessage(body))
	if err != nil {
		var re oauth.RedirectError
		if errors.As(err, &re) {
			log.Printf("oauth: redirect_uri recusado no registro: %q", re.URI)
		}
		if errors.Is(err, oauth.ErrInvalidRedirect) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris vazio, com mais de 5 itens ou com um retorno não permitido.")
			return
		}
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "Metadados do cliente inválidos.")
		return
	}
	id, err := oauth.RandomToken()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL", "Erro interno.")
		return
	}
	c := store.OAuthClient{ID: id, Name: reg.ClientName, RedirectURIs: reg.RedirectURIs, CreatedAt: now}
	if err := s.storeClient(r, c, now); err != nil {
		log.Printf("oauth: registro: %v", err)
		WriteError(w, http.StatusInternalServerError, "INTERNAL", "Erro interno.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  id,
		"client_name":                reg.ClientName,
		"redirect_uris":              reg.RedirectURIs,
		"grant_types":                reg.GrantTypes,
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
		"client_id_issued_at":        now.Unix(),
	})
}

// storeClient limpa os clientes vencidos, abre vaga no teto (apagando o mais
// antigo nunca autorizado) e grava o novo.
func (s *oauthServer) storeClient(r *http.Request, c store.OAuthClient, now time.Time) error {
	db := s.d.Auth.DB
	ctx := r.Context()
	s.regMu.Lock()
	defer s.regMu.Unlock()
	if _, err := db.PruneOAuthClients(ctx, now.Add(-unauthorizedClientTTL)); err != nil {
		return err
	}
	n, err := db.CountUnauthorizedOAuthClients(ctx)
	if err != nil {
		return err
	}
	if n >= s.maxUnauthorized {
		if err := db.DeleteOldestUnauthorizedOAuthClient(ctx); err != nil {
			return err
		}
	}
	return db.CreateOAuthClient(ctx, c)
}
