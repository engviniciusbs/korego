package api

import (
	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/go-chi/chi/v5"
)

// routes guarda os handlers construídos uma vez em newRoutes (alguns têm
// estado: ghH, updateH, wsH) para serem montados em mais de um roteador.
type routes struct {
	d           Deps
	authH       authHandlers
	auditH      auditHandlers
	systemH     systemHandlers
	metricsH    metricsHandlers
	usageH      usageHandlers
	containersH containerHandlers
	pm2H        pm2Handlers
	updateH     *updateHandlers
	wsH         wsHandlers
	ghH         *githubHandlers
	webhookH    *webhookHandlers
}

// mountSessionOnly monta as rotas autenticadas que só valem com sessão
// (nunca por token).
func (rt *routes) mountSessionOnly(r chi.Router) {
	r.Get("/auth/me", rt.authH.me)
	r.Get("/auth/sessions", rt.authH.listSessions)
	r.Delete("/auth/sessions/{id}", rt.authH.revokeSession)
	r.Post("/auth/logout", rt.authH.logout)
	r.Get("/containers/{id}/logs/download", rt.containersH.download)
	r.Get("/ws", rt.wsH.serve)
}

// mountCore monta todas as demais rotas autenticadas. As ações irreversíveis
// passam por gate: pedido por token exige X-Korego-Code; por cookie, nada muda.
func (rt *routes) mountCore(r chi.Router) {
	gate := requireCodeForToken(rt.d.Auth, rt.d.TokenLimiter)
	r.Get("/audit", rt.auditH.list)
	r.Get("/system", rt.systemH.info)
	r.Get("/metrics/current", rt.metricsH.current)
	r.Get("/metrics/history", rt.metricsH.history)
	r.Get("/usage", rt.usageH.current)
	if rt.d.DB != nil {
		r.Get("/usage/at", rt.usageH.at)
		r.Get("/usage/history", rt.usageH.history)
	}
	r.Get("/pm2", rt.pm2H.snapshot)
	r.Get("/containers", rt.containersH.list)
	r.Get("/containers/{id}", rt.containersH.details)
	r.With(gate).Delete("/containers/{id}", rt.containersH.remove)
	r.Post("/containers/{id}/start", rt.containersH.action("start", docker.Client.Start))
	r.Post("/containers/{id}/stop", rt.containersH.action("stop", docker.Client.Stop))
	r.Post("/containers/{id}/restart", rt.containersH.action("restart", docker.Client.Restart))
	r.Get("/containers/{id}/logs", rt.containersH.logs)
	if rt.d.Domains != nil {
		proxyH := proxyHandlers{svc: rt.d.Auth, dom: rt.d.Domains, mig: rt.d.Migrate}
		domainsH := domainHandlers{svc: rt.d.Auth, dom: rt.d.Domains}
		settingsH := settingsHandlers{svc: rt.d.Auth, dom: rt.d.Domains}
		r.Get("/proxy", proxyH.status)
		r.Post("/proxy/retry", proxyH.retry)
		if rt.d.Migrate != nil {
			migH := migrationHandlers{svc: rt.d.Auth, mig: rt.d.Migrate}
			r.Get("/proxy/trial-check", migH.trialCheck)
			r.With(gate).Post("/proxy/takeover", migH.takeover)
			r.With(gate).Post("/proxy/rollback", migH.rollback)
		}
		r.Get("/domains", domainsH.list)
		r.Post("/domains", domainsH.create)
		r.Put("/domains/{id}", domainsH.update)
		r.With(gate).Delete("/domains/{id}", domainsH.remove)
		r.Get("/domains/{id}/dns", domainsH.dns)
		r.Get("/settings/panel", settingsH.panel)
		r.With(gate).Put("/settings/panel/domain", settingsH.setDomain)
		r.With(gate).Put("/settings/panel/public-port", settingsH.setPublicPort)
	}
	if rt.d.Certs != nil {
		certsH := certHandlers{svc: rt.d.Auth, certs: rt.d.Certs}
		r.Get("/certificates", certsH.list)
		r.Post("/certificates", certsH.create)
		r.With(gate).Delete("/certificates/{id}", certsH.remove)
	}
	if rt.d.DB != nil && rt.d.Deploys != nil {
		box := rt.d.Box
		if box == nil {
			box = rt.d.Deploys.Box
		}
		projectsH := projectHandlers{svc: rt.d.Auth, db: rt.d.DB}
		appsH := appHandlers{svc: rt.d.Auth, db: rt.d.DB, deploys: rt.d.Deploys, dataDir: rt.d.DataDir,
			runner: rt.d.Runner, box: box, github: rt.d.GitHubConnected}
		deploysH := deployHandlers{svc: rt.d.Auth, db: rt.d.DB, deploys: rt.d.Deploys, dataDir: rt.d.DataDir}
		r.Get("/projects", projectsH.list)
		r.Post("/projects", projectsH.create)
		r.Put("/projects/{id}", projectsH.update)
		r.With(gate).Delete("/projects/{id}", projectsH.remove)
		r.Get("/apps", appsH.list)
		r.Post("/apps", appsH.create)
		r.Get("/apps/{id}", appsH.get)
		r.Put("/apps/{id}", appsH.update)
		r.With(gate).Delete("/apps/{id}", appsH.remove)
		r.Get("/apps/{id}/env", appsH.envNames)
		r.Put("/apps/{id}/env", appsH.envSet)
		r.Post("/apps/{id}/deploys", deploysH.create)
		r.Get("/apps/{id}/deploys", deploysH.list)
		r.Post("/apps/{id}/rollback", deploysH.rollback)
		r.Get("/deploys/{id}", deploysH.get)
		r.Get("/deploys/{id}/log", deploysH.log)
		r.Post("/deploys/{id}/cancel", deploysH.cancel)
	}
	if rt.ghH != nil {
		r.Get("/github", rt.ghH.status)
		r.With(gate).Delete("/github", rt.ghH.disconnect)
		r.Post("/github/manifest", rt.ghH.manifest)
		r.Get("/github/repos", rt.ghH.repos)
	}
	if rt.d.Updater != nil {
		r.Get("/update", rt.updateH.status)
		r.With(gate).Post("/update", rt.updateH.apply)
	}
}
