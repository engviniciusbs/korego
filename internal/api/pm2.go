package api

import (
	"log"
	"net/http"

	"github.com/engviniciusbs/korego/internal/node"
)

type pm2Handlers struct {
	node node.Node
}

// snapshot serve GET /api/pm2.
func (h pm2Handlers) snapshot(w http.ResponseWriter, r *http.Request) {
	s, err := h.node.PM2().Snapshot(r.Context())
	if err != nil {
		log.Printf("pm2: %v", err)
		WriteError(w, http.StatusServiceUnavailable, "PM2_UNAVAILABLE", "Não foi possível ler os processos do PM2.")
		return
	}
	WriteJSON(w, http.StatusOK, s)
}
