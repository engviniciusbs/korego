package api

import (
	"net/http"
	"time"

	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/node"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/engviniciusbs/korego/internal/version"
	"github.com/go-chi/chi/v5"
)

// Deps agrupa as dependências dos handlers (cresce nas próximas tarefas).
type Deps struct {
	Auth *auth.Service
	Node node.Node
	Hub  *events.Hub
	// WSPingInterval é o intervalo de ping do WebSocket (0 = 30s).
	WSPingInterval time.Duration
	// Web atende tudo fora de /api (nil = 404 simples).
	Web http.Handler
	// Updater consulta e aplica atualizações (nil desativa /api/update).
	Updater Updater
	// Metrics fornece a última amostra de métricas (nil = sempre "não pronto").
	Metrics LatestMetrics
	// DB atende o histórico de métricas.
	DB *store.DB
	// Now é o relógio do histórico de métricas (nil = time.Now).
	Now func() time.Time
}

// NewRouter monta o roteador principal, com a API sob /api.
func NewRouter(d Deps) http.Handler {
	setup := setupHandlers{svc: d.Auth}
	authH := authHandlers{svc: d.Auth}
	auditH := auditHandlers{svc: d.Auth}
	systemH := systemHandlers{node: d.Node}

	metricsH := metricsHandlers{latest: d.Metrics, db: d.DB, now: d.Now}
	updateH := &updateHandlers{svc: d.Auth, up: d.Updater}

	if d.Hub == nil {
		d.Hub = events.NewHub()
	}
	wsH := wsHandlers{svc: d.Auth, hub: d.Hub, pingInterval: d.WSPingInterval}

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
			r.Get("/metrics/current", metricsH.current)
			r.Get("/metrics/history", metricsH.history)
			r.Get("/ws", wsH.serve)
			if d.Updater != nil {
				r.Get("/update", updateH.status)
				r.Post("/update", updateH.apply)
			}
		})

		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Recurso não encontrado.")
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
			WriteError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Método não permitido.")
		})
	})
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if d.Web == nil {
			http.NotFound(w, req)
			return
		}
		d.Web.ServeHTTP(w, req)
	})
	return r
}

func health(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version.Version})
}
