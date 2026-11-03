package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/engviniciusbs/korego/internal/audit"
	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/update"
)

const updateCacheTTL = time.Hour

// Updater abstrai a consulta e a aplicação de atualizações (fake nos testes).
type Updater interface {
	Current() string
	Check(ctx context.Context) (update.Release, error)
	Apply(ctx context.Context, rel update.Release) error
	StartFinish() error
	// Restore devolve o binário anterior quando a finalização não pôde ser agendada.
	Restore() error
}

type updateHandlers struct {
	svc *auth.Service
	up  Updater

	mu      sync.Mutex
	cached  update.Release
	cacheAt time.Time
	hasRel  bool

	// stateMu guarda state: idle -> applying -> applied. "applied" só é
	// limpo por um reinício do processo.
	stateMu sync.Mutex
	state   updateState
}

type updateState int

const (
	updateIdle updateState = iota
	updateApplying
	updateApplied
)

func (h *updateHandlers) setState(s updateState) {
	h.stateMu.Lock()
	h.state = s
	h.stateMu.Unlock()
}

// latest devolve a release em cache (1h) ou consulta de novo. Erros não são cacheados.
func (h *updateHandlers) latest(ctx context.Context, force bool) (update.Release, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !force && h.hasRel && time.Since(h.cacheAt) < updateCacheTTL {
		return h.cached, nil
	}
	rel, err := h.up.Check(ctx)
	if err != nil {
		return update.Release{}, err
	}
	h.cached, h.cacheAt, h.hasRel = rel, time.Now(), true
	return rel, nil
}

// status serves GET /api/update.
func (h *updateHandlers) status(w http.ResponseWriter, r *http.Request) {
	cur := h.up.Current()
	rel, err := h.latest(r.Context(), false)
	if err != nil {
		WriteJSON(w, http.StatusOK, map[string]any{
			"current": cur, "latest": nil, "available": false, "error": err.Error(),
		})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"current": cur, "latest": rel.Version, "available": update.Available(cur, rel.Version),
	})
}

// apply serves POST /api/update.
func (h *updateHandlers) apply(w http.ResponseWriter, r *http.Request) {
	u, ok := CurrentUser(r.Context())
	if !ok || u.Role != "admin" {
		WriteError(w, http.StatusForbidden, "FORBIDDEN", "Apenas administradores podem atualizar o sistema.")
		return
	}
	cur := h.up.Current()
	rel, err := h.latest(r.Context(), false)
	if err != nil || !update.Available(cur, rel.Version) {
		WriteError(w, http.StatusConflict, "NO_UPDATE", "Nenhuma atualização disponível.")
		return
	}
	h.stateMu.Lock()
	if h.state != updateIdle {
		h.stateMu.Unlock()
		WriteError(w, http.StatusConflict, "UPDATE_IN_PROGRESS", "Uma atualização já está em andamento.")
		return
	}
	h.state = updateApplying
	h.stateMu.Unlock()

	if err := h.up.Apply(r.Context(), rel); err != nil {
		h.setState(updateIdle)
		WriteError(w, http.StatusBadGateway, "UPDATE_FAILED", "Falha ao atualizar: "+err.Error())
		return
	}
	if err := h.up.StartFinish(); err != nil {
		if rerr := h.up.Restore(); rerr != nil {
			// exe continua sendo o binário novo: mantém "applied" para bloquear novas tentativas.
			h.setState(updateApplied)
			WriteError(w, http.StatusBadGateway, "UPDATE_FAILED",
				"Falha ao agendar a finalização e ao restaurar o binário anterior; intervenção manual necessária: "+err.Error()+"; "+rerr.Error())
			return
		}
		h.setState(updateIdle)
		WriteError(w, http.StatusBadGateway, "UPDATE_FAILED", "Falha ao agendar a finalização da atualização: "+err.Error())
		return
	}
	h.setState(updateApplied)
	ip, _ := clientInfo(r)
	h.svc.Record(r.Context(), audit.Entry{
		UserID: &u.ID, Action: "system.update", IP: ip,
		Metadata: map[string]any{"from": cur, "to": rel.Version},
	})
	WriteJSON(w, http.StatusAccepted, map[string]string{"status": "updating", "from": cur, "to": rel.Version})
}
