package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/deploy"
	"github.com/engviniciusbs/korego/internal/store"
)

const (
	defaultDeployLimit = 20
	maxDeployLimit     = 100
	maxLogEntries      = 5000
)

type deployHandlers struct {
	svc     *auth.Service
	db      *store.DB
	deploys *deploy.Service
	dataDir string
}

type deployDTO struct {
	ID            int64      `json:"id"`
	AppID         int64      `json:"appId"`
	Trigger       string     `json:"trigger"`
	Status        string     `json:"status"`
	Reason        string     `json:"reason"`
	CommitSHA     string     `json:"commitSha"`
	CommitMessage string     `json:"commitMessage"`
	Image         string     `json:"image"`
	HostPort      int        `json:"hostPort"`
	RollbackOf    *int64     `json:"rollbackOf"`
	CreatedAt     time.Time  `json:"createdAt"`
	StartedAt     *time.Time `json:"startedAt"`
	FinishedAt    *time.Time `json:"finishedAt"`
}

func toDeployDTO(d store.Deploy) deployDTO {
	return deployDTO{
		ID: d.ID, AppID: d.AppID, Trigger: d.Trigger, Status: d.Status, Reason: d.Reason,
		CommitSHA: d.CommitSHA, CommitMessage: d.CommitMessage, Image: d.Image, HostPort: d.HostPort,
		RollbackOf: d.RollbackOf, CreatedAt: d.CreatedAt, StartedAt: d.StartedAt, FinishedAt: d.FinishedAt,
	}
}

func deployID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	return pathID(w, r, "id", "INVALID_DEPLOY_ID", "ID de deploy inválido.")
}

// deployError traduz os erros do serviço de deploy. notFound é o código 404
// do recurso que provavelmente faltou.
func deployError(w http.ResponseWriter, err error, notFound string) {
	switch {
	case errors.Is(err, deploy.ErrNoGitHub):
		WriteError(w, http.StatusConflict, "NO_GITHUB", "Conecte o GitHub antes de publicar esta aplicação.")
	case errors.Is(err, deploy.ErrRollbackUnsupported):
		WriteError(w, http.StatusConflict, "ROLLBACK_NOT_SUPPORTED", "Aplicações Compose não suportam rollback.")
	case errors.Is(err, deploy.ErrImageGone):
		WriteError(w, http.StatusConflict, "IMAGE_GONE", "A imagem desse deploy não está mais disponível.")
	case errors.Is(err, deploy.ErrDeployFinished):
		WriteError(w, http.StatusConflict, "DEPLOY_FINISHED", "O deploy já terminou.")
	case errors.Is(err, store.ErrNotFound):
		msg := "Aplicação não encontrada."
		if notFound == "DEPLOY_NOT_FOUND" {
			msg = "Deploy não encontrado."
		}
		WriteError(w, http.StatusNotFound, notFound, msg)
	default:
		internalError(w, err)
	}
}

func (h deployHandlers) slugOf(r *http.Request, appID int64, fallback string) string {
	if a, err := h.db.GetApp(r.Context(), appID); err == nil {
		return a.Slug
	}
	return fallback
}

// create serves POST /api/apps/{id}/deploys.
func (h deployHandlers) create(w http.ResponseWriter, r *http.Request) {
	id, ok := appID(w, r)
	if !ok {
		return
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	target := h.slugOf(r, id, strconv.FormatInt(id, 10))
	d, err := h.deploys.Deploy(ctx, id, "manual", "", "")
	if err != nil {
		record(h.svc, r, "deploy.create", target, map[string]any{"trigger": "manual", "ok": false})
		deployError(w, err, "APP_NOT_FOUND")
		return
	}
	record(h.svc, r, "deploy.create", target, map[string]any{"trigger": "manual", "ok": true})
	WriteJSON(w, http.StatusAccepted, map[string]any{"deploy": toDeployDTO(d)})
}

// list serves GET /api/apps/{id}/deploys?limit=20.
func (h deployHandlers) list(w http.ResponseWriter, r *http.Request) {
	id, ok := appID(w, r)
	if !ok {
		return
	}
	limit := defaultDeployLimit
	if q := r.URL.Query().Get("limit"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 || n > maxDeployLimit {
			WriteError(w, http.StatusBadRequest, "INVALID_LIMIT", "O limite deve ficar entre 1 e 100.")
			return
		}
		limit = n
	}
	if _, err := h.db.GetApp(r.Context(), id); err != nil {
		deployError(w, err, "APP_NOT_FOUND")
		return
	}
	ds, err := h.db.ListDeploys(r.Context(), id, limit)
	if err != nil {
		internalError(w, err)
		return
	}
	out := make([]deployDTO, len(ds))
	for i, d := range ds {
		out[i] = toDeployDTO(d)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"deploys": out})
}

// get serves GET /api/deploys/{id}.
func (h deployHandlers) get(w http.ResponseWriter, r *http.Request) {
	id, ok := deployID(w, r)
	if !ok {
		return
	}
	d, err := h.db.GetDeploy(r.Context(), id)
	if err != nil {
		deployError(w, err, "DEPLOY_NOT_FOUND")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"deploy": toDeployDTO(d)})
}

// log serves GET /api/deploys/{id}/log?from=N.
func (h deployHandlers) log(w http.ResponseWriter, r *http.Request) {
	id, ok := deployID(w, r)
	if !ok {
		return
	}
	from := 0
	if q := r.URL.Query().Get("from"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 0 {
			WriteError(w, http.StatusBadRequest, "INVALID_FROM", "O parâmetro from deve ser um inteiro maior ou igual a 0.")
			return
		}
		from = n
	}
	d, err := h.db.GetDeploy(r.Context(), id)
	if err != nil {
		deployError(w, err, "DEPLOY_NOT_FOUND")
		return
	}
	// Lê o fim do log antes das linhas: se done, nada foi escrito depois.
	finished := terminalStatus(d.Status) && deploy.LogFinished(h.dataDir, id)
	entries, err := deploy.ReadLog(h.dataDir, id, from)
	if err != nil {
		internalError(w, err)
		return
	}
	if len(entries) > maxLogEntries {
		entries, finished = entries[:maxLogEntries], false
	}
	type entry struct {
		N    int    `json:"n"`
		Text string `json:"text"`
	}
	out := make([]entry, len(entries))
	for i, e := range entries {
		out[i] = entry{N: e.N, Text: e.Text}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"entries": out, "done": finished})
}

// cancel serves POST /api/deploys/{id}/cancel.
func (h deployHandlers) cancel(w http.ResponseWriter, r *http.Request) {
	id, ok := deployID(w, r)
	if !ok {
		return
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	target := strconv.FormatInt(id, 10)
	if d, err := h.db.GetDeploy(ctx, id); err == nil {
		target = h.slugOf(r, d.AppID, target)
	}
	if err := h.deploys.Cancel(ctx, id); err != nil {
		record(h.svc, r, "deploy.cancel", target, map[string]any{"deployId": id, "ok": false})
		deployError(w, err, "DEPLOY_NOT_FOUND")
		return
	}
	record(h.svc, r, "deploy.cancel", target, map[string]any{"deployId": id, "ok": true})
	w.WriteHeader(http.StatusNoContent)
}

// rollback serves POST /api/apps/{id}/rollback com {"deployId": n}.
func (h deployHandlers) rollback(w http.ResponseWriter, r *http.Request) {
	id, ok := appID(w, r)
	if !ok {
		return
	}
	var in struct {
		DeployID int64 `json:"deployId"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.DeployID <= 0 {
		WriteError(w, http.StatusBadRequest, "INVALID_DEPLOY_ID", "ID de deploy inválido.")
		return
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	target := h.slugOf(r, id, strconv.FormatInt(id, 10))
	if _, err := h.db.GetApp(ctx, id); err != nil {
		record(h.svc, r, "deploy.rollback", target, map[string]any{"from": in.DeployID, "ok": false})
		deployError(w, err, "APP_NOT_FOUND")
		return
	}
	d, err := h.deploys.Rollback(ctx, id, in.DeployID)
	if err != nil {
		record(h.svc, r, "deploy.rollback", target, map[string]any{"from": in.DeployID, "ok": false})
		deployError(w, err, "DEPLOY_NOT_FOUND")
		return
	}
	record(h.svc, r, "deploy.rollback", target, map[string]any{"from": in.DeployID, "ok": true})
	WriteJSON(w, http.StatusAccepted, map[string]any{"deploy": toDeployDTO(d)})
}
