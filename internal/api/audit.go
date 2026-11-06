package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/engviniciusbs/korego/internal/auth"
)

const (
	defaultAuditLimit = 50
	maxAuditLimit     = 200
)

type auditJSON struct {
	ID        int64           `json:"id"`
	UserID    *int64          `json:"userId"`
	Action    string          `json:"action"`
	Target    string          `json:"target"`
	IP        string          `json:"ip"`
	Metadata  json.RawMessage `json:"metadata"`
	CreatedAt time.Time       `json:"createdAt"`
}

type auditHandlers struct{ svc *auth.Service }

// list serves GET /api/audit?limit=50&before=<id>, newest first.
func (h auditHandlers) list(w http.ResponseWriter, r *http.Request) {
	limit, ok := queryInt(r, "limit", defaultAuditLimit)
	if !ok || limit < 1 {
		WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Parâmetro limit inválido.")
		return
	}
	limit = min(limit, maxAuditLimit)
	before, ok := queryInt(r, "before", 0)
	if !ok || before < 0 {
		WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Parâmetro before inválido.")
		return
	}
	rows, err := h.svc.DB.ListAudit(r.Context(), limit, before)
	if err != nil {
		internalError(w, err)
		return
	}
	items := make([]auditJSON, 0, len(rows))
	for _, a := range rows {
		meta := json.RawMessage(a.Metadata)
		if !json.Valid(meta) {
			meta = json.RawMessage("{}")
		}
		items = append(items, auditJSON{
			ID: a.ID, UserID: a.UserID, Action: a.Action, Target: a.Target,
			IP: a.IP, Metadata: meta, CreatedAt: a.CreatedAt,
		})
	}
	var next *int64
	if int64(len(items)) == limit {
		next = &items[len(items)-1].ID
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "nextBefore": next})
}

func queryInt(r *http.Request, name string, def int64) (int64, bool) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def, true
	}
	v, err := strconv.ParseInt(s, 10, 64)
	return v, err == nil
}
