package domains

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/store"
)

// SettingPublicPort guarda se a porta do painel escuta em todas as
// interfaces ("true", padrão quando ausente) ou só em 127.0.0.1 ("false").
const SettingPublicPort = "panel.public_port"

// ErrPanelNotHTTPS recusa desligar a porta pública sem que o painel esteja
// sendo acessado pelo próprio domínio, com HTTPS e certificado ativo.
var ErrPanelNotHTTPS = errors.New("acesse o painel pelo domínio com HTTPS antes de desativar a porta pública")

// ErrPanelPortOff recusa alterar ou remover o domínio do painel com a porta
// pública desligada: o domínio é o único acesso ao painel.
var ErrPanelPortOff = errors.New("Reative a porta 8000 pública antes de alterar ou remover o domínio do painel.")

// PanelSettings são as configurações de acesso ao painel.
type PanelSettings struct {
	Domain     string         `json:"domain"`
	DomainID   int64          `json:"domainId"`
	Cert       proxy.CertInfo `json:"cert"`
	PublicPort bool           `json:"publicPort"`
	ListenAddr string         `json:"listenAddr"` // endereço de escuta efetivo
}

// PublicPort lê panel.public_port; ausente ou diferente de "false" = ligada.
func PublicPort(ctx context.Context, db *store.DB) (bool, error) {
	v, ok, err := db.GetSetting(ctx, SettingPublicPort)
	if err != nil {
		return false, err
	}
	return !ok || v != "false", nil
}

// EffectiveListenAddr devolve listenAddr com a porta pública ligada e
// 127.0.0.1:<porta de listenAddr> com ela desligada.
func EffectiveListenAddr(listenAddr string, public bool) (string, error) {
	_, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "", fmt.Errorf("endereço de escuta inválido %q: %w", listenAddr, err)
	}
	if public {
		return listenAddr, nil
	}
	return net.JoinHostPort("127.0.0.1", port), nil
}

// ListenPort devolve a porta de listenAddr.
func ListenPort(listenAddr string) (int, error) {
	_, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return 0, fmt.Errorf("endereço de escuta inválido %q: %w", listenAddr, err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return 0, fmt.Errorf("porta inválida em %q: %w", listenAddr, err)
	}
	return n, nil
}

// Panel devolve o domínio do painel (com o certificado) e o estado da porta.
func (s *Service) Panel(ctx context.Context) (PanelSettings, error) {
	var p PanelSettings
	d, ok, err := s.DB.PanelDomain(ctx)
	if err != nil {
		return p, err
	}
	if ok {
		p.Domain, p.DomainID = d.Hostname, d.ID
		p.Cert = s.Proxy.Cert(ctx, d.Hostname)
	}
	if p.PublicPort, err = PublicPort(ctx, s.DB); err != nil {
		return p, err
	}
	if p.ListenAddr, err = EffectiveListenAddr(s.ListenAddr, p.PublicPort); err != nil {
		return p, err
	}
	return p, nil
}

// SetPanelDomain define o domínio do painel (vazio remove) e recarrega o
// proxy. Há no máximo uma linha is_panel: trocar o host atualiza a mesma e
// mantém as opções (certificado, só Cloudflare, cabeçalhos, compressão); com
// certificado próprio, ele precisa cobrir o novo host. Proxy inativo não é
// erro. Com a porta pública desligada, só é permitido regravar o mesmo host
// (outro host ou vazio → ErrPanelPortOff).
func (s *Service) SetPanelDomain(ctx context.Context, host string) error {
	s.panelMu.Lock()
	defer s.panelMu.Unlock()

	host = strings.TrimSpace(host)
	cur, exists, err := s.DB.PanelDomain(ctx)
	if err != nil {
		return err
	}
	public, err := PublicPort(ctx, s.DB)
	if err != nil {
		return err
	}
	if !public && (!exists || host != cur.Hostname) {
		return ErrPanelPortOff
	}
	if host == "" {
		if !exists {
			return nil
		}
		if err := s.DB.DeleteDomain(ctx, cur.ID); err != nil {
			return err
		}
		return s.reapplyPanel(ctx)
	}

	if !ValidHostname(host) {
		return invalid("hostname", "Hostname inválido. Use letras minúsculas, números e hífens, como painel.exemplo.com.")
	}
	if err := s.CheckCollision(ctx, host, WWWNone, cur.ID); err != nil {
		return err
	}
	// Nova linha: certificado automático e compressão ligada (padrão dos
	// domínios novos). Linha existente: mantém as opções.
	d := store.Domain{TLSMode: proxy.TLSAuto, Compression: true, Headers: []store.Header{}}
	if exists {
		d = cur
		if d.TLSMode == proxy.TLSCustom {
			if _, _, _, err := s.validTLS(ctx, TLSInput{Mode: proxy.TLSCustom, CertID: d.CertID}, []string{host}); err != nil {
				return err
			}
		}
	}
	d.Hostname, d.WWWRedirect, d.ForceHTTPS, d.IsPanel = host, WWWNone, true, true
	d.Routes = s.panelRoutes()
	d.TargetType, d.AppID, d.Address = TargetAddress, nil, net.JoinHostPort("127.0.0.1", strconv.Itoa(s.PanelPort))
	if exists {
		err = s.DB.UpdateDomain(ctx, &d)
	} else {
		err = s.DB.InsertDomain(ctx, &d)
	}
	if err != nil {
		return err
	}
	return s.reapplyPanel(ctx)
}

// updatePanel altera as opções do domínio do painel (PUT /api/domains/{id}):
// certificado, só Cloudflare, cabeçalhos e compressão. A rota é fixa (rotas
// no corpo → INVALID_ROUTE), o hostname só muda pelas configurações do painel
// (outro hostname → ErrPanelDomain) e os demais campos do corpo são
// ignorados. Com a porta pública desligada → ErrPanelPortOff: o domínio é o
// único acesso ao painel.
func (s *Service) updatePanel(ctx context.Context, id int64, in DomainInput) (store.Domain, error) {
	s.panelMu.Lock()
	defer s.panelMu.Unlock()

	cur, err := s.DB.GetDomain(ctx, id)
	if err != nil {
		return store.Domain{}, err
	}
	if in.Routes != nil {
		return store.Domain{}, invalidCode(CodeInvalidRoute, "routes", "As rotas do domínio do painel são fixas.")
	}
	if h := strings.TrimSpace(in.Hostname); h != "" && h != cur.Hostname {
		return store.Domain{}, ErrPanelDomain
	}
	public, err := PublicPort(ctx, s.DB)
	if err != nil {
		return store.Domain{}, err
	}
	if !public {
		return store.Domain{}, ErrPanelPortOff
	}
	d := cur
	d.Routes = s.panelRoutes()
	d.TargetType, d.AppID, d.Address = rootTarget(d.Routes)
	pemText, err := s.applyOptions(ctx, &d, in)
	if err != nil {
		return store.Domain{}, err
	}
	if in.Compression != nil {
		d.Compression = *in.Compression
	}
	if err := s.buildCheck(d, pemText); err != nil {
		return store.Domain{}, err
	}
	if err := s.DB.UpdateDomain(ctx, &d); err != nil {
		return store.Domain{}, err
	}
	s.reapplyAfterSave(ctx)
	return d, nil
}

func (s *Service) reapplyPanel(ctx context.Context) error {
	if err := s.Reapply(ctx); err != nil && !errors.Is(err, proxy.ErrInactive) {
		return fmt.Errorf("aplicar domínio do painel no proxy: %w", err)
	}
	return nil
}

// SetPublicPort liga ou desliga a porta pública do painel. Desligar exige
// viaPanelHTTPS (a requisição veio pelo domínio do painel com HTTPS) e o
// certificado ativo; senão ErrPanelNotHTTPS. Religar é sempre permitido.
// Grava a configuração e chama OnListenChange com o novo endereço; se a troca
// falhar, restaura o valor anterior e devolve o erro.
func (s *Service) SetPublicPort(ctx context.Context, enabled bool, viaPanelHTTPS bool) error {
	s.panelMu.Lock()
	defer s.panelMu.Unlock()

	if !enabled {
		d, ok, err := s.DB.PanelDomain(ctx)
		if err != nil {
			return err
		}
		if !ok || !viaPanelHTTPS || s.Proxy.Cert(ctx, d.Hostname).State != proxy.CertActive {
			return ErrPanelNotHTTPS
		}
	}
	addr, err := EffectiveListenAddr(s.ListenAddr, enabled)
	if err != nil {
		return err
	}

	prev, hadPrev, err := s.DB.GetSetting(ctx, SettingPublicPort)
	if err != nil {
		return err
	}
	if err := s.DB.SetSetting(ctx, SettingPublicPort, strconv.FormatBool(enabled)); err != nil {
		return err
	}
	if s.OnListenChange == nil {
		return nil
	}
	if err := s.OnListenChange(addr); err != nil {
		var rerr error
		if hadPrev {
			rerr = s.DB.SetSetting(ctx, SettingPublicPort, prev)
		} else {
			rerr = s.DB.DeleteSetting(ctx, SettingPublicPort)
		}
		if rerr != nil {
			return errors.Join(fmt.Errorf("trocar endereço de escuta para %s: %w", addr, err), rerr)
		}
		return fmt.Errorf("trocar endereço de escuta para %s: %w", addr, err)
	}
	return nil
}
