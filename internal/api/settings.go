package api

import (
	"net"
	"net/http"
	"strings"

	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/domains"
)

type settingsHandlers struct {
	svc *auth.Service
	dom *domains.Service
}

// stripPort remove a porta de um Host.
func stripPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return strings.Trim(host, "[]")
}

type panelBody struct {
	domains.PanelSettings
	RequestHTTPS bool   `json:"requestHTTPS"`
	RequestHost  string `json:"requestHost"`
}

func (h settingsHandlers) writePanel(w http.ResponseWriter, r *http.Request, status int) {
	p, err := h.dom.Panel(r.Context())
	if err != nil {
		internalError(w, err)
		return
	}
	WriteJSON(w, status, panelBody{
		PanelSettings: p, RequestHTTPS: requestIsHTTPS(r), RequestHost: stripPort(r.Host),
	})
}

// panel serves GET /api/settings/panel.
func (h settingsHandlers) panel(w http.ResponseWriter, r *http.Request) {
	h.writePanel(w, r, http.StatusOK)
}

// panelDomainInput é o corpo de PUT /settings/panel/domain.
type panelDomainInput struct {
	Hostname string `json:"hostname"`
}

// panelPortInput é o corpo de PUT /settings/panel/public-port.
type panelPortInput struct {
	Enabled bool `json:"enabled"`
}

// setDomain serves PUT /api/settings/panel/domain.
func (h settingsHandlers) setDomain(w http.ResponseWriter, r *http.Request) {
	var in panelDomainInput
	if !decodeJSON(w, r, &in) {
		return
	}
	host := strings.TrimSpace(in.Hostname)
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	if err := h.dom.SetPanelDomain(ctx, host); err != nil {
		// Falha do proxy depois de gravar: o domínio ficou salvo.
		if !isDomainErr(err) {
			if p, perr := h.dom.Panel(ctx); perr == nil && strings.EqualFold(p.Domain, host) {
				record(h.svc, r, "panel.domain", host, map[string]any{"hostname": host, "ok": true})
				WriteJSON(w, http.StatusOK, map[string]any{"saved": true, "proxyError": err.Error()})
				return
			}
		}
		record(h.svc, r, "panel.domain", host, map[string]any{"hostname": host, "ok": false})
		domainError(w, err)
		return
	}
	record(h.svc, r, "panel.domain", host, map[string]any{"hostname": host, "ok": true})
	h.writePanel(w, r, http.StatusOK)
}

// setPublicPort serves PUT /api/settings/panel/public-port.
func (h settingsHandlers) setPublicPort(w http.ResponseWriter, r *http.Request) {
	var in panelPortInput
	if !decodeJSON(w, r, &in) {
		return
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	p, err := h.dom.Panel(ctx)
	if err != nil {
		internalError(w, err)
		return
	}
	via := requestIsHTTPS(r) && p.Domain != "" && strings.EqualFold(stripPort(r.Host), p.Domain)
	if err := h.dom.SetPublicPort(ctx, in.Enabled, via); err != nil {
		record(h.svc, r, "panel.public_port", "panel", map[string]any{"enabled": in.Enabled, "ok": false})
		domainError(w, err)
		return
	}
	record(h.svc, r, "panel.public_port", "panel", map[string]any{"enabled": in.Enabled, "ok": true})
	h.writePanel(w, r, http.StatusOK)
}
