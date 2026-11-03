package api

import (
	"net/http"

	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/node"
	"github.com/engviniciusbs/korego/internal/version"
	"github.com/go-chi/chi/v5"
)

// Deps agrupa as dependências dos handlers (cresce nas próximas tarefas).
type Deps struct {
	Auth *auth.Service
	Node node.Node
}

// NewRouter monta o roteador principal, com a API sob /api.
func NewRouter(d Deps) http.Handler {
	setup := setupHandlers{svc: d.Auth}
	authH := authHandlers{svc: d.Auth}
	auditH := auditHandlers{svc: d.Auth}
	systemH := systemHandlers{node: d.Node}

	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		// Públicas.
		r.Get("/health", health)
		r.Get("/setup/status", setup.status)
		r.Post("/setup", setup.begin)
		r.Post("/setup/confirm", setup.confirm)
		r.Post("/auth/login", authH.login)

		// Autenticadas; RequireCSRF ignora GET/HEAD/OPTIONS.
		r.Group(func(r chi.Router) {
			r.Use(RequireSession(d.Auth), RequireCSRF)
			r.Get("/auth/me", authH.me)
			r.Get("/auth/sessions", authH.listSessions)
			r.Delete("/auth/sessions/{id}", authH.revokeSession)
			r.Post("/auth/logout", authH.logout)
			r.Get("/audit", auditH.list)
			r.Get("/system", systemH.info)
		})

		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Recurso não encontrado.")
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
			WriteError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Método não permitido.")
		})
	})
	return r
}

func health(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version.Version})
}
