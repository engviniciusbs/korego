package api

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/domains"
	"github.com/engviniciusbs/korego/internal/migrate"
	"github.com/engviniciusbs/korego/internal/node/proxy"
)

// guideURL é o guia de migração do nginx, mostrado quando o proxy está bloqueado.
const guideURL = "https://github.com/engviniciusbs/korego/blob/main/docs/migracao-nginx.md"

type proxyHandlers struct {
	svc *auth.Service
	dom *domains.Service
	mig *migrate.Engine // nil = sem migração
}

type proxyStatusBody struct {
	proxy.Status
	GuideURL string `json:"guideUrl"`
	// Migração: servidor anterior (null se não houve), quando, o último
	// resultado de assumir/voltar, se há troca em andamento e se Voltar está
	// disponível (unit registrada e o arquivo dela existe).
	MigratedFrom      *string                `json:"migratedFrom"`
	MigratedAt        *time.Time             `json:"migratedAt"`
	LastMigration     *migrate.LastMigration `json:"lastMigration"`
	Migrating         bool                   `json:"migrating"`
	RollbackAvailable bool                   `json:"rollbackAvailable"`
}

func (h proxyHandlers) body(ctx context.Context, st proxy.Status) proxyStatusBody {
	b := proxyStatusBody{Status: st, GuideURL: guideURL}
	if h.mig == nil {
		return b
	}
	info, err := h.mig.Info(ctx)
	if err != nil {
		log.Printf("proxy: ler o estado da migração: %v", err)
	}
	if info.MigratedFrom != "" {
		b.MigratedFrom = &info.MigratedFrom
	}
	b.MigratedAt, b.LastMigration = info.MigratedAt, info.Last
	b.Migrating, b.RollbackAvailable = info.Running, info.RollbackAvailable
	return b
}

// status serves GET /api/proxy.
func (h proxyHandlers) status(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, h.body(r.Context(), h.dom.Status()))
}

// retry serves POST /api/proxy/retry: uma passada imediata de reconciliação.
func (h proxyHandlers) retry(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	st := h.dom.Reconcile(ctx)
	record(h.svc, r, "proxy.retry", "proxy", map[string]any{"ok": true, "state": st.State})
	WriteJSON(w, http.StatusOK, h.body(ctx, st))
}
