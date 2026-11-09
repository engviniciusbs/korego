package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/certs"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/go-chi/chi/v5"
)

type certHandlers struct {
	svc   *auth.Service
	certs *certs.Service
}

// certDTO é o certificado na API. Nunca inclui o PEM nem a chave.
type certDTO struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Issuer      string    `json:"issuer"`
	Names       []string  `json:"names"`
	NotAfter    time.Time `json:"notAfter"`
	Fingerprint string    `json:"fingerprint"`
	UsedBy      []string  `json:"usedBy"`
	ExpiresSoon bool      `json:"expiresSoon"`
}

func toCertDTO(v certs.View) certDTO {
	d := certDTO{
		ID: v.ID, Name: v.Name, Issuer: v.Issuer, Names: v.Names, NotAfter: v.NotAfter,
		Fingerprint: v.Fingerprint, UsedBy: v.UsedBy, ExpiresSoon: v.ExpiresSoon,
	}
	if d.Names == nil {
		d.Names = []string{}
	}
	if d.UsedBy == nil {
		d.UsedBy = []string{}
	}
	return d
}

// certError traduz os erros do serviço de certificados para a resposta HTTP.
func certError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, certs.ErrDuplicate), errors.Is(err, certs.ErrInvalidCert):
		WriteError(w, http.StatusBadRequest, "INVALID_CERT", err.Error())
	case errors.Is(err, certs.ErrKeyMismatch):
		WriteError(w, http.StatusBadRequest, "CERT_MISMATCH", err.Error())
	case errors.Is(err, certs.ErrExpired):
		WriteError(w, http.StatusBadRequest, "CERT_EXPIRED", err.Error())
	case errors.Is(err, store.ErrCertInUse):
		WriteError(w, http.StatusBadRequest, "CERT_IN_USE",
			"Este certificado está em uso por um domínio. Troque o certificado do domínio antes de apagar.")
	case errors.Is(err, store.ErrNotFound):
		WriteError(w, http.StatusNotFound, "CERT_NOT_FOUND", "Certificado não encontrado.")
	default:
		internalError(w, err)
	}
}

// list serves GET /api/certificates.
func (h certHandlers) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.certs.List(r.Context())
	if err != nil {
		internalError(w, err)
		return
	}
	out := make([]certDTO, len(list))
	for i, v := range list {
		out[i] = toCertDTO(v)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"certificates": out})
}

type certInput struct {
	Name string `json:"name"`
	PEM  string `json:"pem"`
	Path string `json:"path"`
}

// create serves POST /api/certificates com {name, pem} ou {name, path}.
func (h certHandlers) create(w http.ResponseWriter, r *http.Request) {
	var in certInput
	if !decodeJSON(w, r, &in) {
		return
	}
	target := strings.TrimSpace(in.Name)
	if len(target) > 100 {
		target = target[:100]
	}
	fail := map[string]any{"ok": false}
	if in.Path != "" {
		fail["path"] = in.Path
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	var (
		c   store.Certificate
		err error
	)
	switch {
	case in.PEM != "" && in.Path != "":
		WriteError(w, http.StatusBadRequest, "INVALID_CERT", "Envie o PEM ou o caminho do arquivo, não os dois.")
		record(h.svc, r, "certificate.create", target, fail)
		return
	case in.Path != "":
		c, err = h.certs.AddFromPath(ctx, in.Name, in.Path)
	case in.PEM != "":
		c, err = h.certs.Add(ctx, in.Name, []byte(in.PEM))
	default:
		record(h.svc, r, "certificate.create", target, fail)
		WriteError(w, http.StatusBadRequest, "INVALID_CERT", "Informe o PEM ou o caminho do arquivo.")
		return
	}
	if err != nil {
		record(h.svc, r, "certificate.create", target, fail)
		certError(w, err)
		return
	}
	record(h.svc, r, "certificate.create", c.Name,
		okMeta(c, in.Path))
	WriteJSON(w, http.StatusCreated, map[string]any{"certificate": toCertDTO(h.certs.ViewOf(c, nil))})
}

func okMeta(c store.Certificate, path string) map[string]any {
	m := map[string]any{"ok": true, "name": c.Name, "fingerprint": c.Fingerprint}
	if path != "" {
		m["path"] = path
	}
	return m
}

// remove serves DELETE /api/certificates/{id}.
func (h certHandlers) remove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusBadRequest, "INVALID_CERT_ID", "ID de certificado inválido.")
		return
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	meta := map[string]any{"ok": false}
	target := strconv.FormatInt(id, 10)
	if cur, err := h.certs.Get(ctx, id); err == nil {
		target = cur.Name
		meta["name"], meta["fingerprint"] = cur.Name, cur.Fingerprint
	}
	if err := h.certs.Delete(ctx, id); err != nil {
		record(h.svc, r, "certificate.delete", target, meta)
		certError(w, err)
		return
	}
	meta["ok"] = true
	record(h.svc, r, "certificate.delete", target, meta)
	w.WriteHeader(http.StatusNoContent)
}
