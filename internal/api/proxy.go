package api

import (
	"net/http"

	"github.com/engviniciusbs/korego/internal/domains"
	"github.com/engviniciusbs/korego/internal/node/proxy"
)

// guideURL é o guia de migração do nginx, mostrado quando o proxy está bloqueado.
const guideURL = "https://github.com/engviniciusbs/korego/blob/main/docs/migracao-nginx.md"

type proxyHandlers struct {
	dom *domains.Service
}

type proxyStatusBody struct {
	proxy.Status
	GuideURL string `json:"guideUrl"`
}

// status serves GET /api/proxy.
func (h proxyHandlers) status(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, proxyStatusBody{Status: h.dom.Status(), GuideURL: guideURL})
}

// retry serves POST /api/proxy/retry: uma passada imediata de reconciliação.
func (h proxyHandlers) retry(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	WriteJSON(w, http.StatusOK, proxyStatusBody{Status: h.dom.Reconcile(ctx), GuideURL: guideURL})
}
