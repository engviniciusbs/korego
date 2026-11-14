package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// CoreForTest devolve um roteador só com o núcleo de rotas autenticadas
// (sem sessão nem CSRF), montado na raiz.
func CoreForTest(d Deps) http.Handler {
	r := chi.NewRouter()
	newRoutes(d).mountCore(r)
	return r
}
