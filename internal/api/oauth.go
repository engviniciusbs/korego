package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/engviniciusbs/korego/internal/auth"
)

const (
	// maxUnauthorizedClients é o teto de clientes que nunca foram autorizados.
	maxUnauthorizedClients = 200
	// unauthorizedClientTTL é quanto um cliente nunca autorizado sobrevive.
	unauthorizedClientTTL = 30 * 24 * time.Hour
)

// oauthServer é o servidor de autorização OAuth 2.1 do painel: metadados,
// registro dinâmico e (Tasks 6 e 7) authorize/approve e token.
type oauthServer struct {
	d      Deps
	origin originFunc
	now    func() time.Time

	regLim   *auth.Limiter // registros por IP
	authzLim *auth.Limiter // /oauth/authorize por IP (Task 6)
	tokenLim *auth.Limiter // falhas no /oauth/token por IP (Task 7)

	// maxUnauthorized é o teto de clientes nunca autorizados (os testes baixam).
	maxUnauthorized int
	// regMu serializa limpeza, despejo e inserção, para o teto valer sob concorrência.
	regMu sync.Mutex

	pending *pendingStore // pedidos de autorização aguardando consentimento
	codes   *codeStore    // códigos de autorização emitidos
}

// newOAuthServer monta o servidor; origin diz a origem canônica (ou que não há).
func newOAuthServer(d Deps, origin originFunc) *oauthServer {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &oauthServer{
		d: d, origin: origin, now: now,
		regLim:          auth.NewLimiter(10, time.Hour, time.Hour),
		authzLim:        auth.NewLimiter(30, time.Minute, 5*time.Minute),
		tokenLim:        auth.NewLimiter(10, time.Minute, 15*time.Minute),
		maxUnauthorized: maxUnauthorizedClients,
		pending:         newPendingStore(),
		codes:           newCodeStore(),
	}
}

type oauthOriginKey struct{}

// originFrom devolve a origem que requireOrigin deixou no contexto.
func originFrom(ctx context.Context) string {
	o, _ := ctx.Value(oauthOriginKey{}).(string)
	return o
}

// requireOrigin dá 404 sem domínio do painel com certificado e 403 fora do
// HTTPS; do contrário, passa a origem canônica no contexto. Nunca usa r.Host.
func (s *oauthServer) requireOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o, ok := s.origin(r.Context())
		if !ok {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Recurso não encontrado.")
			return
		}
		if !requestIsHTTPS(r) {
			WriteError(w, http.StatusForbidden, "TOKEN_REQUIRES_HTTPS", msgTokenHTTPS)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), oauthOriginKey{}, o)))
	})
}

// mountPublic monta, fora de /api, os endpoints sem cookie nem CSRF. Não há
// CORS: não existe cliente de navegador.
func (s *oauthServer) mountPublic(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(s.requireOrigin)
		r.Get("/.well-known/oauth-protected-resource", s.protectedResource)
		r.Get("/.well-known/oauth-protected-resource/mcp", s.protectedResource)
		r.Get("/.well-known/oauth-authorization-server", s.serverMetadata)
		r.Post("/oauth/register", s.register)
		r.Get("/oauth/authorize", s.authorize)
		r.Post("/oauth/token", s.token)
	})
}

// writeOAuthError escreve o erro no formato do RFC 6749 §5.2.
func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

func (s *oauthServer) protectedResource(w http.ResponseWriter, r *http.Request) {
	o := originFrom(r.Context())
	WriteJSON(w, http.StatusOK, oauthex.ProtectedResourceMetadata{
		Resource:               o + "/mcp",
		AuthorizationServers:   []string{o},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "KoreGO",
	})
}

func (s *oauthServer) serverMetadata(w http.ResponseWriter, r *http.Request) {
	o := originFrom(r.Context())
	meta := oauthex.AuthServerMeta{
		Issuer:                                     o,
		AuthorizationEndpoint:                      o + "/oauth/authorize",
		TokenEndpoint:                              o + "/oauth/token",
		RegistrationEndpoint:                       o + "/oauth/register",
		ResponseTypesSupported:                     []string{"code"},
		GrantTypesSupported:                        []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported:              []string{"S256"},
		TokenEndpointAuthMethodsSupported:          []string{"none"},
		AuthorizationResponseIssParameterSupported: true,
	}
	// O tipo do SDK sempre emite "jwks_uri": ""; não temos JWKS, então o campo sai.
	raw, _ := json.Marshal(meta)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	delete(m, "jwks_uri")
	WriteJSON(w, http.StatusOK, m)
}
