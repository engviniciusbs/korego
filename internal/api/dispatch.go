package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/go-chi/chi/v5"
)

// newCore monta o núcleo de rotas autenticadas sob /api, sem sessão nem CSRF,
// para o despacho interno. A identidade vem do contexto (withTokenIdentity).
func newCore(rt *routes) http.Handler {
	notFound := func(w http.ResponseWriter, _ *http.Request) {
		WriteError(w, http.StatusNotFound, "NOT_FOUND", "Recurso não encontrado.")
	}
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		rt.mountCore(r)
		r.NotFound(notFound)
		r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
			WriteError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Método não permitido.")
		})
	})
	r.NotFound(notFound)
	return r
}

// dispatch runs one request through the core router in-process.
type dispatcher struct{ core http.Handler }

// do monta a requisição (path com /api), copia o endereço e os cabeçalhos de
// identidade de id, põe X-Korego-Code se code != "", reaplica a identidade no
// contexto e serve pelo núcleo. body só vira JSON quando não é nil.
func (d dispatcher) do(ctx context.Context, id tokenIdentity, method, path string, query url.Values,
	body any, code string) (status int, resp []byte) {
	rec := httptest.NewRecorder()
	var rd io.Reader = http.NoBody
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			internalError(rec, err)
			return rec.Code, rec.Body.Bytes()
		}
		rd = bytes.NewReader(b)
	}
	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		internalError(rec, err)
		return rec.Code, rec.Body.Bytes()
	}
	req.RemoteAddr = id.RemoteAddr
	for k, vs := range id.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if code != "" {
		req.Header.Set(codeHeader, code)
	}
	// Uma confirmação de código nunca vem de fora: só o gate a marca.
	ictx := context.WithValue(ctx, codeConfirmedKey, false)
	d.core.ServeHTTP(rec, req.WithContext(withTokenIdentity(ictx, id)))
	return rec.Code, rec.Body.Bytes()
}
