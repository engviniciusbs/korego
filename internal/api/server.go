package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/certs"
	"github.com/engviniciusbs/korego/internal/deploy"
	"github.com/engviniciusbs/korego/internal/domains"
	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/github"
	"github.com/engviniciusbs/korego/internal/migrate"
	"github.com/engviniciusbs/korego/internal/node"
	"github.com/engviniciusbs/korego/internal/secrets"
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
	// Now é o relógio do histórico de métricas e dos states do GitHub (nil = time.Now).
	Now func() time.Time
	// Usage fornece o último retrato de consumo por dono (nil = sempre "não pronto").
	Usage LatestUsage
	// Domains atende /api/proxy, /api/domains e /api/settings/panel (nil as desativa).
	Domains *domains.Service
	// Migrate atende /api/proxy/trial-check, /takeover e /rollback (nil as
	// desativa; exige Domains).
	Migrate *migrate.Engine
	// Certs atende /api/certificates (nil a desativa).
	Certs *certs.Service
	// Deploys executa e consulta os deploys das aplicações.
	Deploys *deploy.Service
	// DataDir guarda logs e diretórios das aplicações (apagados com a app).
	DataDir string
	// Runner executa "docker compose down" ao apagar uma app Compose (nil = ExecRunner).
	Runner deploy.Runner
	// Box cifra as variáveis de ambiente das aplicações (nil = a do serviço de deploy).
	Box *secrets.Box
	// GitHubConnected diz se há GitHub App conectado (nil = não).
	GitHubConnected func(context.Context) bool
	// GitHub atende /api/github e o webhook (nil as desativa).
	GitHub *github.Service
	// Hostname compõe o nome do GitHub App no manifesto (nil = os.Hostname).
	Hostname func() (string, error)
	// Tokens valida os tokens de API dos agentes (nil desativa /mcp).
	Tokens *auth.Tokens
	// TokenLimiter limita, por "token:"+IP, tokens inválidos e códigos
	// errados vindos de tokens (nil = sem limite).
	TokenLimiter *auth.Limiter
	// OAuthOrigin, se não for nil, substitui a origem derivada de Domains
	// (serve aos testes). Em produção fica nil e a origem vem de panelOrigin.
	OAuthOrigin originFunc
}

// newRoutes constrói uma vez todos os handlers autenticados (alguns têm estado).
func newRoutes(d Deps) *routes {
	authH := authHandlers{svc: d.Auth}
	auditH := auditHandlers{svc: d.Auth}
	systemH := systemHandlers{node: d.Node}

	metricsH := metricsHandlers{latest: d.Metrics, db: d.DB, now: d.Now}
	usageH := usageHandlers{latest: d.Usage, db: d.DB, now: d.Now}
	containersH := containerHandlers{svc: d.Auth, node: d.Node}
	pm2H := pm2Handlers{node: d.Node}
	updateH := &updateHandlers{svc: d.Auth, up: d.Updater, mig: d.Migrate}

	if d.Hub == nil {
		d.Hub = events.NewHub()
	}
	wsH := wsHandlers{svc: d.Auth, hub: d.Hub, pingInterval: d.WSPingInterval}

	var ghH *githubHandlers
	var webhookH *webhookHandlers
	if d.GitHub != nil {
		hostname := d.Hostname
		if hostname == nil {
			hostname = defaultHostname
		}
		ghH = &githubHandlers{svc: d.Auth, gh: d.GitHub, dom: d.Domains, states: newStateStore(d.Now),
			hostname: hostname, connectMu: &sync.Mutex{}}
		if d.Deploys != nil {
			webhookH = &webhookHandlers{svc: d.Auth, gh: d.GitHub, deploys: d.Deploys,
				deliveries: newDeliverySet(maxDeliveries)}
		}
	}

	return &routes{d: d, authH: authH, auditH: auditH, systemH: systemH, metricsH: metricsH, usageH: usageH,
		containersH: containersH, pm2H: pm2H, updateH: updateH, wsH: wsH, ghH: ghH, webhookH: webhookH}
}

// NewRouter monta o roteador principal, com a API sob /api.
func NewRouter(d Deps) http.Handler {
	rt := newRoutes(d)
	d = rt.d
	setup := setupHandlers{svc: d.Auth}
	authH := rt.authH
	webhookH, ghH := rt.webhookH, rt.ghH

	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		// Públicas.
		r.Get("/health", health)
		r.Get("/setup/status", setup.status)
		r.Post("/setup", setup.begin)
		r.Post("/setup/confirm", setup.confirm)
		r.Post("/auth/login", authH.login)
		if ghH != nil {
			r.Get("/github/callback", ghH.callback)
		}
		if webhookH != nil {
			r.Post("/webhooks/github", webhookH.github)
		}

		// Autenticadas; RequireCSRF ignora GET/HEAD/OPTIONS.
		r.Group(func(r chi.Router) {
			r.Use(RequireSession(d.Auth), RequireCSRF)
			rt.mountSessionOnly(r)
			rt.mountCore(r)
		})

		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Recurso não encontrado.")
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
			WriteError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Método não permitido.")
		})
	})
	// MCP: só Bearer (nunca cookie); as ferramentas despacham no núcleo.
	if d.Tokens != nil && d.Auth != nil {
		mcpH := newMCPHandler(dispatcher{core: newCore(rt)}, version.Version)
		r.With(RequireToken(d.Tokens, d.Auth.DB, d.TokenLimiter, resolveOrigin(d))).Handle("/mcp", mcpH)
	}
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
