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
// proxy. Há no máximo uma linha is_panel: trocar o host atualiza a mesma.
// Proxy inativo não é erro.
func (s *Service) SetPanelDomain(ctx context.Context, host string) error {
	s.panelMu.Lock()
	defer s.panelMu.Unlock()

	host = strings.TrimSpace(host)
	cur, exists, err := s.DB.PanelDomain(ctx)
	if err != nil {
		return err
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
	d := store.Domain{
		ID:          cur.ID,
		Hostname:    host,
		TargetType:  TargetAddress,
		Address:     net.JoinHostPort("127.0.0.1", strconv.Itoa(s.PanelPort)),
		WWWRedirect: WWWNone,
		ForceHTTPS:  true,
		IsPanel:     true,
		CreatedAt:   cur.CreatedAt,
	}
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
