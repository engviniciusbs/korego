package api

import (
	"net/http"

	"github.com/engviniciusbs/korego/internal/version"
	"github.com/go-chi/chi/v5"
)

// Deps agrupa as dependências dos handlers (cresce nas próximas tarefas).
type Deps struct{}

// NewRouter monta o roteador principal, com a API sob /api.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Get("/health", health)
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
