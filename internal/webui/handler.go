package webui

import (
	"bytes"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

const notBuiltMsg = "Frontend não compilado. Rode `make web`."

// NewHandler serve os arquivos estáticos do export do frontend.
func NewHandler(fsys fs.FS) http.Handler {
	return &handler{fsys: fsys}
}

type handler struct{ fsys fs.FS }

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Método não permitido.", http.StatusMethodNotAllowed)
		return
	}
	if !isFile(h.fsys, "index.html") {
		http.Error(w, notBuiltMsg, http.StatusServiceUnavailable)
		return
	}

	// path.Clean sobre um caminho absoluto elimina ".." antes de entrar no FS.
	clean := path.Clean("/" + r.URL.Path)
	name := strings.TrimPrefix(clean, "/")
	if name == "" {
		name = "."
	}

	if isFile(h.fsys, name) {
		h.serveFile(w, r, name)
		return
	}
	if name == "." || isDir(h.fsys, name) {
		idx := path.Join(name, "index.html")
		if name == "." {
			idx = "index.html"
		}
		if isFile(h.fsys, idx) {
			if !strings.HasSuffix(r.URL.Path, "/") {
				target := clean + "/"
				if r.URL.RawQuery != "" {
					target += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, target, http.StatusMovedPermanently)
				return
			}
			h.serveFile(w, r, idx)
			return
		}
	}
	h.notFound(w, r)
}

func (h *handler) notFound(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(h.fsys, "404.html")
	if err != nil {
		http.Error(w, "Não encontrado.", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusNotFound)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func (h *handler) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	data, err := fs.ReadFile(h.fsys, name)
	if err != nil {
		h.notFound(w, r)
		return
	}
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = http.DetectContentType(data)
	}
	w.Header().Set("Content-Type", ct)
	if strings.HasPrefix(name, "_next/static/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else if strings.HasPrefix(ct, "text/html") {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}

func isFile(fsys fs.FS, name string) bool {
	st, err := fs.Stat(fsys, name)
	return err == nil && !st.IsDir()
}

func isDir(fsys fs.FS, name string) bool {
	st, err := fs.Stat(fsys, name)
	return err == nil && st.IsDir()
}
