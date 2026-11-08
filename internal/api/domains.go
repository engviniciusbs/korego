package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/engviniciusbs/korego/internal/audit"
	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/domains"
	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/go-chi/chi/v5"
)

type domainHandlers struct {
	svc *auth.Service
	dom *domains.Service
}

// domainDTO é o domínio na API, em camelCase.
type domainDTO struct {
	ID          int64          `json:"id"`
	Hostname    string         `json:"hostname"`
	TargetType  string         `json:"targetType"`
	AppID       *int64         `json:"appId"`
	AppSlug     string         `json:"appSlug"`
	Address     string         `json:"address"`
	WWWRedirect string         `json:"wwwRedirect"`
	ForceHTTPS  bool           `json:"forceHttps"`
	IsPanel     bool           `json:"isPanel"`
	Cert        proxy.CertInfo `json:"cert"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}

func toDomainDTO(d store.Domain, cert proxy.CertInfo, slug string) domainDTO {
	return domainDTO{
		ID: d.ID, Hostname: d.Hostname, TargetType: d.TargetType, AppID: d.AppID, AppSlug: slug,
		Address: d.Address, WWWRedirect: d.WWWRedirect, ForceHTTPS: d.ForceHTTPS, IsPanel: d.IsPanel,
		Cert: cert, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

// dnsDTO é o resultado da verificação de DNS na API.
type dnsDTO struct {
	OK       bool     `json:"ok"`
	Resolved []string `json:"resolved"`
	Expected []string `json:"expected"`
	Message  string   `json:"message"`
}

func toDNSDTO(c domains.DNSCheck) dnsDTO {
	out := dnsDTO{OK: c.OK, Resolved: c.Resolved, Expected: c.Expected, Message: c.Message}
	if out.Resolved == nil {
		out.Resolved = []string{}
	}
	if out.Expected == nil {
		out.Expected = []string{}
	}
	return out
}

// domainError traduz os erros do serviço de domínios para a resposta HTTP.
func domainError(w http.ResponseWriter, err error) {
	var inv *domains.ErrInvalid
	switch {
	case errors.As(err, &inv):
		writeFieldError(w, http.StatusBadRequest, "INVALID_DOMAIN", inv.Message, inv.Field)
	case errors.Is(err, store.ErrConflict):
		WriteError(w, http.StatusConflict, "DOMAIN_EXISTS", "Este domínio já está cadastrado.")
	case errors.Is(err, domains.ErrNotFound):
		WriteError(w, http.StatusNotFound, "DOMAIN_NOT_FOUND", "Domínio não encontrado.")
	case errors.Is(err, domains.ErrPanelDomain):
		WriteError(w, http.StatusConflict, "PANEL_DOMAIN_PROTECTED",
			"O domínio do painel só pode ser alterado nas configurações do painel.")
	case errors.Is(err, domains.ErrPanelNotHTTPS):
		WriteError(w, http.StatusConflict, "PANEL_NOT_HTTPS",
			"Acesse o painel pelo domínio com HTTPS antes de desativar a porta 8000.")
	case errors.Is(err, domains.ErrPanelPortOff):
		WriteError(w, http.StatusConflict, "PANEL_PORT_OFF", err.Error())
	default:
		internalError(w, err)
	}
}

// isDomainErr diz se err é uma recusa do serviço (e não uma falha posterior
// à gravação).
func isDomainErr(err error) bool {
	var inv *domains.ErrInvalid
	return errors.As(err, &inv) || errors.Is(err, domains.ErrPanelPortOff) ||
		errors.Is(err, domains.ErrPanelDomain) || errors.Is(err, store.ErrConflict) ||
		errors.Is(err, domains.ErrNotFound)
}

// domainID valida o {id} da rota; senão escreve 400 INVALID_DOMAIN_ID.
func domainID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusBadRequest, "INVALID_DOMAIN_ID", "ID de domínio inválido.")
		return 0, false
	}
	return id, true
}

// record grava a auditoria sem depender do cliente continuar conectado.
func record(svc *auth.Service, r *http.Request, action, target string, meta map[string]any) {
	sess, ok := CurrentSession(r.Context())
	if !ok {
		return
	}
	ctx, cancel := detached(r, auditTimeout)
	defer cancel()
	svc.Record(ctx, audit.Entry{
		UserID: &sess.UserID, Action: action, Target: target, IP: auth.ClientIP(r), Metadata: meta,
	})
}

func (h domainHandlers) view(ctx context.Context, d store.Domain) domainDTO {
	slug := ""
	if d.AppID != nil {
		if a, err := h.dom.DB.GetApp(ctx, *d.AppID); err == nil {
			slug = a.Slug
		}
	}
	return toDomainDTO(d, h.dom.Proxy.Cert(ctx, d.Hostname), slug)
}

// list serves GET /api/domains.
func (h domainHandlers) list(w http.ResponseWriter, r *http.Request) {
	list, err := h.dom.List(r.Context())
	if err != nil {
		internalError(w, err)
		return
	}
	out := make([]domainDTO, len(list))
	for i, v := range list {
		out[i] = toDomainDTO(v.Domain, v.Cert, v.AppSlug)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"domains": out})
}

func (h domainHandlers) save(w http.ResponseWriter, r *http.Request, action string, status int,
	call func(ctx context.Context, in domains.DomainInput) (store.Domain, domains.DNSCheck, error)) {
	var in domains.DomainInput
	if !decodeJSON(w, r, &in) {
		return
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	d, check, err := call(ctx, in)
	if err != nil {
		record(h.svc, r, action, in.Hostname, map[string]any{"ok": false})
		domainError(w, err)
		return
	}
	record(h.svc, r, action, d.Hostname, map[string]any{"ok": true})
	WriteJSON(w, status, map[string]any{"domain": h.view(ctx, d), "dns": toDNSDTO(check)})
}

// create serves POST /api/domains.
func (h domainHandlers) create(w http.ResponseWriter, r *http.Request) {
	h.save(w, r, "domain.create", http.StatusCreated,
		func(ctx context.Context, in domains.DomainInput) (store.Domain, domains.DNSCheck, error) {
			return h.dom.Create(ctx, in)
		})
}

// update serves PUT /api/domains/{id}.
func (h domainHandlers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := domainID(w, r)
	if !ok {
		return
	}
	h.save(w, r, "domain.update", http.StatusOK,
		func(ctx context.Context, in domains.DomainInput) (store.Domain, domains.DNSCheck, error) {
			return h.dom.Update(ctx, id, in)
		})
}

// remove serves DELETE /api/domains/{id}.
func (h domainHandlers) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := domainID(w, r)
	if !ok {
		return
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	target := strconv.FormatInt(id, 10)
	if cur, err := h.dom.DB.GetDomain(ctx, id); err == nil {
		target = cur.Hostname
	}
	if err := h.dom.Delete(ctx, id); err != nil {
		record(h.svc, r, "domain.delete", target, map[string]any{"ok": false})
		domainError(w, err)
		return
	}
	record(h.svc, r, "domain.delete", target, map[string]any{"ok": true})
	w.WriteHeader(http.StatusNoContent)
}

// dns serves GET /api/domains/{id}/dns.
func (h domainHandlers) dns(w http.ResponseWriter, r *http.Request) {
	id, ok := domainID(w, r)
	if !ok {
		return
	}
	d, err := h.dom.DB.GetDomain(r.Context(), id)
	if err != nil {
		domainError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, toDNSDTO(h.dom.CheckDNS(r.Context(), d.Hostname)))
}
