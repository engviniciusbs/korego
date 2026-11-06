package api

import (
	"bufio"
	"context"
	"errors"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/engviniciusbs/korego/internal/audit"
	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/node"
	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/go-chi/chi/v5"
)

const (
	defaultLogTail = 500
	maxLogTail     = 5000
)

var containerIDRe = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

type containerHandlers struct {
	svc  *auth.Service
	node node.Node
}

// validID escreve 400 INVALID_CONTAINER_ID e devolve "" se o {id} for inválido.
func validID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	if !containerIDRe.MatchString(id) {
		WriteError(w, http.StatusBadRequest, "INVALID_CONTAINER_ID", "ID de container inválido.")
		return "", false
	}
	return id, true
}

// dockerError traduz um erro do cliente Docker para a resposta HTTP.
func dockerError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, docker.ErrUnavailable),
		errors.Is(err, context.DeadlineExceeded) && r.Context().Err() == nil:
		WriteError(w, http.StatusServiceUnavailable, "DOCKER_UNAVAILABLE", "Não foi possível conectar ao Docker.")
	case errors.Is(err, docker.ErrNotFound):
		WriteError(w, http.StatusNotFound, "CONTAINER_NOT_FOUND", "Container não encontrado.")
	case errors.Is(err, docker.ErrConflict):
		WriteError(w, http.StatusConflict, "CONTAINER_RUNNING", "O container precisa estar parado para ser removido.")
	default:
		internalError(w, err)
	}
}

// list serves GET /api/containers.
func (h containerHandlers) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.node.Docker().List(r.Context())
	if err != nil {
		dockerError(w, r, err)
		return
	}
	if list == nil {
		list = []docker.Container{}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Project != list[j].Project {
			return list[i].Project < list[j].Project
		}
		return list[i].Name < list[j].Name
	})
	WriteJSON(w, http.StatusOK, map[string]any{"containers": list})
}

// details serves GET /api/containers/{id}.
func (h containerHandlers) details(w http.ResponseWriter, r *http.Request) {
	id, ok := validID(w, r)
	if !ok {
		return
	}
	d, err := h.node.Docker().Inspect(r.Context(), id)
	if err != nil {
		dockerError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, d)
}

func (h containerHandlers) audit(r *http.Request, action, id string, d docker.Details, ok bool) {
	sess, has := CurrentSession(r.Context())
	if !has {
		return
	}
	target := d.Name
	if target == "" {
		target = id
	}
	h.svc.Record(r.Context(), audit.Entry{
		UserID: &sess.UserID, Action: action, Target: target, IP: auth.ClientIP(r),
		Metadata: map[string]any{"id": id, "managed": d.Managed, "ok": ok},
	})
}

// action monta o handler de start, stop e restart.
func (h containerHandlers) action(name string, call func(docker.Client, context.Context, string) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := validID(w, r)
		if !ok {
			return
		}
		c := h.node.Docker()
		d, err := c.Inspect(r.Context(), id)
		if err != nil {
			dockerError(w, r, err)
			return
		}
		if err := call(c, r.Context(), id); err != nil {
			h.audit(r, "container."+name, id, d, false)
			dockerError(w, r, err)
			return
		}
		h.audit(r, "container."+name, id, d, true)
		w.WriteHeader(http.StatusNoContent)
	}
}

func notRemovable(state string) bool {
	return state == "running" || state == "restarting" || state == "paused"
}

// remove serves DELETE /api/containers/{id}: só com o container parado.
func (h containerHandlers) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := validID(w, r)
	if !ok {
		return
	}
	c := h.node.Docker()
	d, err := c.Inspect(r.Context(), id)
	if err != nil {
		dockerError(w, r, err)
		return
	}
	if notRemovable(d.State) {
		h.audit(r, "container.remove", id, d, false)
		dockerError(w, r, docker.ErrConflict)
		return
	}
	if err := c.Remove(r.Context(), id); err != nil {
		h.audit(r, "container.remove", id, d, false)
		dockerError(w, r, err)
		return
	}
	h.audit(r, "container.remove", id, d, true)
	w.WriteHeader(http.StatusNoContent)
}

// logs serves GET /api/containers/{id}/logs?tail=N.
func (h containerHandlers) logs(w http.ResponseWriter, r *http.Request) {
	id, ok := validID(w, r)
	if !ok {
		return
	}
	tail := int64(defaultLogTail)
	if s := r.URL.Query().Get("tail"); s != "" {
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil || v < 1 || v > maxLogTail {
			WriteError(w, http.StatusBadRequest, "INVALID_TAIL", "O parâmetro tail deve estar entre 1 e 5000.")
			return
		}
		tail = v
	}
	lines := []docker.LogLine{}
	err := h.node.Docker().Logs(r.Context(), id, docker.LogOptions{Tail: int(tail)}, func(l docker.LogLine) {
		lines = append(lines, l)
	})
	if err != nil {
		dockerError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

// safeFilename deixa só caracteres seguros para o cabeçalho.
func safeFilename(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			return r
		}
		return '_'
	}, s)
}

// download serves GET /api/containers/{id}/logs/download, em streaming.
func (h containerHandlers) download(w http.ResponseWriter, r *http.Request) {
	id, ok := validID(w, r)
	if !ok {
		return
	}
	c := h.node.Docker()
	d, err := c.Inspect(r.Context(), id)
	if err != nil {
		dockerError(w, r, err)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var bw *bufio.Writer
	commit := func() {
		if bw != nil {
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+safeFilename(d.Name)+`-logs.txt"`)
		bw = bufio.NewWriterSize(w, 32*1024)
	}
	var werr error
	err = c.Logs(ctx, id, docker.LogOptions{}, func(l docker.LogLine) {
		if werr != nil {
			return
		}
		commit()
		ts := l.TS
		if n, perr := strconv.ParseInt(l.TS, 10, 64); perr == nil {
			ts = time.Unix(0, n).UTC().Format(time.RFC3339Nano)
		}
		if _, werr = bw.WriteString(ts + " " + l.Stream + " " + l.Text + "\n"); werr != nil {
			cancel()
		}
	})
	if bw == nil {
		if err != nil {
			dockerError(w, r, err)
			return
		}
		commit()
	}
	if err != nil && ctx.Err() == nil {
		// Cabeçalhos já enviados: só dá para registrar e encerrar o corpo.
		log.Printf("erro ao baixar logs de %s: %v", id, err)
	}
	if werr == nil {
		_ = bw.Flush()
	}
}
