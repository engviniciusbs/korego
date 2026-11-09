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
	"github.com/engviniciusbs/korego/internal/node"
	"github.com/engviniciusbs/korego/internal/node/docker"
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
}

// NewRouter monta o roteador principal, com a API sob /api.
func NewRouter(d Deps) http.Handler {
	setup := setupHandlers{svc: d.Auth}
	authH := authHandlers{svc: d.Auth}
	auditH := auditHandlers{svc: d.Auth}
	systemH := systemHandlers{node: d.Node}

	metricsH := metricsHandlers{latest: d.Metrics, db: d.DB, now: d.Now}
	usageH := usageHandlers{latest: d.Usage, db: d.DB, now: d.Now}
	containersH := containerHandlers{svc: d.Auth, node: d.Node}
	pm2H := pm2Handlers{node: d.Node}
	updateH := &updateHandlers{svc: d.Auth, up: d.Updater}

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
			r.Get("/auth/me", authH.me)
			r.Get("/auth/sessions", authH.listSessions)
			r.Delete("/auth/sessions/{id}", authH.revokeSession)
			r.Post("/auth/logout", authH.logout)
			r.Get("/audit", auditH.list)
			r.Get("/system", systemH.info)
			r.Get("/metrics/current", metricsH.current)
			r.Get("/metrics/history", metricsH.history)
			r.Get("/usage", usageH.current)
			if d.DB != nil {
				r.Get("/usage/at", usageH.at)
				r.Get("/usage/history", usageH.history)
			}
			r.Get("/pm2", pm2H.snapshot)
			r.Get("/containers", containersH.list)
			r.Get("/containers/{id}", containersH.details)
			r.Delete("/containers/{id}", containersH.remove)
			r.Post("/containers/{id}/start", containersH.action("start", docker.Client.Start))
			r.Post("/containers/{id}/stop", containersH.action("stop", docker.Client.Stop))
			r.Post("/containers/{id}/restart", containersH.action("restart", docker.Client.Restart))
			r.Get("/containers/{id}/logs", containersH.logs)
			r.Get("/containers/{id}/logs/download", containersH.download)
			r.Get("/ws", wsH.serve)
			if d.Domains != nil {
				proxyH := proxyHandlers{dom: d.Domains}
				domainsH := domainHandlers{svc: d.Auth, dom: d.Domains}
				settingsH := settingsHandlers{svc: d.Auth, dom: d.Domains}
				r.Get("/proxy", proxyH.status)
				r.Post("/proxy/retry", proxyH.retry)
				r.Get("/domains", domainsH.list)
				r.Post("/domains", domainsH.create)
				r.Put("/domains/{id}", domainsH.update)
				r.Delete("/domains/{id}", domainsH.remove)
				r.Get("/domains/{id}/dns", domainsH.dns)
				r.Get("/settings/panel", settingsH.panel)
				r.Put("/settings/panel/domain", settingsH.setDomain)
				r.Put("/settings/panel/public-port", settingsH.setPublicPort)
			}
			if d.Certs != nil {
				certsH := certHandlers{svc: d.Auth, certs: d.Certs}
				r.Get("/certificates", certsH.list)
				r.Post("/certificates", certsH.create)
				r.Delete("/certificates/{id}", certsH.remove)
			}
			if d.DB != nil && d.Deploys != nil {
				box := d.Box
				if box == nil {
					box = d.Deploys.Box
				}
				projectsH := projectHandlers{svc: d.Auth, db: d.DB}
				appsH := appHandlers{svc: d.Auth, db: d.DB, deploys: d.Deploys, dataDir: d.DataDir,
					runner: d.Runner, box: box, github: d.GitHubConnected}
				deploysH := deployHandlers{svc: d.Auth, db: d.DB, deploys: d.Deploys, dataDir: d.DataDir}
				r.Get("/projects", projectsH.list)
				r.Post("/projects", projectsH.create)
				r.Put("/projects/{id}", projectsH.update)
				r.Delete("/projects/{id}", projectsH.remove)
				r.Get("/apps", appsH.list)
				r.Post("/apps", appsH.create)
				r.Get("/apps/{id}", appsH.get)
				r.Put("/apps/{id}", appsH.update)
				r.Delete("/apps/{id}", appsH.remove)
				r.Get("/apps/{id}/env", appsH.envNames)
				r.Put("/apps/{id}/env", appsH.envSet)
				r.Post("/apps/{id}/deploys", deploysH.create)
				r.Get("/apps/{id}/deploys", deploysH.list)
				r.Post("/apps/{id}/rollback", deploysH.rollback)
				r.Get("/deploys/{id}", deploysH.get)
				r.Get("/deploys/{id}/log", deploysH.log)
				r.Post("/deploys/{id}/cancel", deploysH.cancel)
			}
			if ghH != nil {
				r.Get("/github", ghH.status)
				r.Delete("/github", ghH.disconnect)
				r.Post("/github/manifest", ghH.manifest)
				r.Get("/github/repos", ghH.repos)
			}
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
