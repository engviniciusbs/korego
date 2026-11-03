package api

import (
	"net/http"

	"github.com/engviniciusbs/korego/internal/node"
)

type systemHandlers struct {
	node node.Node
}

func (h systemHandlers) info(w http.ResponseWriter, r *http.Request) {
	info, err := h.node.Info(r.Context())
	if err != nil {
		WriteError(w, http.StatusServiceUnavailable, "NODE_UNAVAILABLE", "Não foi possível ler as informações do servidor.")
		return
	}
	WriteJSON(w, http.StatusOK, info)
}
