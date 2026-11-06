package api

import (
	"context"
	"time"

	"net/http"

	"github.com/engviniciusbs/korego/internal/events"
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

// SystemSource publica {"uptimeSeconds": n} no tópico "system": uma vez ao
// iniciar e depois a cada interval. Em erro do node, o tick é ignorado.
func SystemSource(n node.Node, interval time.Duration) events.SourceFunc {
	return func(ctx context.Context, _ string, publish func(data any)) {
		tick := func() {
			info, err := n.Info(ctx)
			if err != nil {
				return
			}
			publish(map[string]any{"uptimeSeconds": info.UptimeSeconds})
		}
		tick()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				tick()
			}
		}
	}
}
