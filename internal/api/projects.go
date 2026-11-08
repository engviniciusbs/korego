package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/go-chi/chi/v5"
)

type projectHandlers struct {
	svc *auth.Service
	db  *store.DB
}

// projectInput é o corpo de POST/PUT /api/projects.
type projectInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type projectDTO struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	AppCount    int       `json:"appCount"`
	CreatedAt   time.Time `json:"createdAt"`
}

func toProjectDTO(p store.Project, apps int) projectDTO {
	return projectDTO{ID: p.ID, Name: p.Name, Description: p.Description, AppCount: apps, CreatedAt: p.CreatedAt}
}

// pathID lê um {param} inteiro positivo; senão escreve 400 com o código dado.
func pathID(w http.ResponseWriter, r *http.Request, param, code, msg string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, param), 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusBadRequest, code, msg)
		return 0, false
	}
	return id, true
}

func projectID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	return pathID(w, r, "id", "INVALID_PROJECT_ID", "ID de projeto inválido.")
}

// validate normaliza e valida o corpo; devolve o campo e a mensagem do erro.
func (in *projectInput) validate() (field, msg string) {
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	switch {
	case in.Name == "" || len([]rune(in.Name)) > 100:
		return "name", "O nome do projeto deve ter de 1 a 100 caracteres."
	case len([]rune(in.Description)) > 500:
		return "description", "A descrição deve ter até 500 caracteres."
	}
	return "", ""
}

// list serves GET /api/projects.
func (h projectHandlers) list(w http.ResponseWriter, r *http.Request) {
	ps, err := h.db.ListProjects(r.Context())
	if err != nil {
		internalError(w, err)
		return
	}
	apps, err := h.db.ListApps(r.Context(), nil)
	if err != nil {
		internalError(w, err)
		return
	}
	count := map[int64]int{}
	for _, a := range apps {
		count[a.ProjectID]++
	}
	out := make([]projectDTO, len(ps))
	for i, p := range ps {
		out[i] = toProjectDTO(p, count[p.ID])
	}
	WriteJSON(w, http.StatusOK, map[string]any{"projects": out})
}

func (h projectHandlers) storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrConflict):
		WriteError(w, http.StatusConflict, "PROJECT_EXISTS", "Já existe um projeto com esse nome.")
	case errors.Is(err, store.ErrNotFound):
		WriteError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "Projeto não encontrado.")
	default:
		internalError(w, err)
	}
}

// create serves POST /api/projects.
func (h projectHandlers) create(w http.ResponseWriter, r *http.Request) {
	var in projectInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if f, m := in.validate(); f != "" {
		record(h.svc, r, "project.create", in.Name, map[string]any{"ok": false})
		writeFieldError(w, http.StatusBadRequest, "INVALID_PROJECT", m, f)
		return
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	p := store.Project{Name: in.Name, Description: in.Description}
	if err := h.db.InsertProject(ctx, &p); err != nil {
		record(h.svc, r, "project.create", in.Name, map[string]any{"ok": false})
		h.storeError(w, err)
		return
	}
	record(h.svc, r, "project.create", p.Name, map[string]any{"ok": true})
	WriteJSON(w, http.StatusCreated, map[string]any{"project": toProjectDTO(p, 0)})
}

// update serves PUT /api/projects/{id}.
func (h projectHandlers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := projectID(w, r)
	if !ok {
		return
	}
	var in projectInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if f, m := in.validate(); f != "" {
		record(h.svc, r, "project.update", in.Name, map[string]any{"ok": false})
		writeFieldError(w, http.StatusBadRequest, "INVALID_PROJECT", m, f)
		return
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	p := store.Project{ID: id, Name: in.Name, Description: in.Description}
	if err := h.db.UpdateProject(ctx, &p); err != nil {
		record(h.svc, r, "project.update", in.Name, map[string]any{"ok": false})
		h.storeError(w, err)
		return
	}
	cur, err := h.db.GetProject(ctx, id)
	if err != nil {
		h.storeError(w, err)
		return
	}
	apps, _ := h.db.ListApps(ctx, &id)
	record(h.svc, r, "project.update", cur.Name, map[string]any{"ok": true})
	WriteJSON(w, http.StatusOK, map[string]any{"project": toProjectDTO(cur, len(apps))})
}

// remove serves DELETE /api/projects/{id}.
func (h projectHandlers) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := projectID(w, r)
	if !ok {
		return
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	target := strconv.FormatInt(id, 10)
	if cur, err := h.db.GetProject(ctx, id); err == nil {
		target = cur.Name
	}
	if err := h.db.DeleteProject(ctx, id); err != nil {
		record(h.svc, r, "project.delete", target, map[string]any{"ok": false})
		if errors.Is(err, store.ErrConflict) {
			WriteError(w, http.StatusConflict, "PROJECT_NOT_EMPTY",
				"O projeto possui aplicações. Remova-as antes de apagar o projeto.")
			return
		}
		h.storeError(w, err)
		return
	}
	record(h.svc, r, "project.delete", target, map[string]any{"ok": true})
	w.WriteHeader(http.StatusNoContent)
}
