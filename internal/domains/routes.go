package domains

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/store"
)

// wwwMode converte o valor gravado no banco na constante do proxy.
func wwwMode(v string) string {
	switch v {
	case WWWToApex:
		return proxy.WWWToApex
	case WWWApexToWWW:
		return proxy.WWWApexToWWW
	default:
		return proxy.WWWNone
	}
}

func localUpstream(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// Routes monta as rotas do proxy a partir dos domínios gravados:
//   - painel → 127.0.0.1:<PanelPort> (fica de fora se PanelPort ≤ 0);
//   - app → 127.0.0.1:<host_port>, só com host_port > 0 e app existente;
//   - endereço manual → como está.
func (s *Service) Routes(ctx context.Context) ([]proxy.Route, error) {
	list, err := s.DB.ListDomains(ctx)
	if err != nil {
		return nil, fmt.Errorf("montar rotas: %w", err)
	}
	apps, err := s.appsByID(ctx)
	if err != nil {
		return nil, fmt.Errorf("montar rotas: %w", err)
	}
	routes := []proxy.Route{}
	for _, d := range list {
		var upstream string
		switch {
		case d.IsPanel:
			if s.PanelPort > 0 {
				upstream = localUpstream(s.PanelPort)
			}
		case d.TargetType == TargetApp:
			if d.AppID != nil {
				if a, ok := apps[*d.AppID]; ok && a.HostPort > 0 {
					upstream = localUpstream(a.HostPort)
				}
			}
		default:
			upstream = d.Address
		}
		if upstream == "" {
			continue
		}
		routes = append(routes, proxy.Route{Host: d.Hostname, Upstream: upstream,
			ForceHTTPS: d.ForceHTTPS, WWWRedirect: wwwMode(d.WWWRedirect)})
	}
	return routes, nil
}

func (s *Service) appsByID(ctx context.Context) (map[int64]store.App, error) {
	apps, err := s.DB.ListApps(ctx, nil)
	if err != nil {
		return nil, err
	}
	m := make(map[int64]store.App, len(apps))
	for _, a := range apps {
		m[a.ID] = a
	}
	return m, nil
}

// Reapply monta as rotas e as carrega no proxy. Devolve proxy.ErrInactive
// se o proxy não estiver ativo.
func (s *Service) Reapply(ctx context.Context) error {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	routes, err := s.Routes(ctx)
	if err != nil {
		return err
	}
	return s.Proxy.Apply(ctx, routes)
}

// ReapplyApp é chamado pelo deploy depois de trocar a porta da app.
func (s *Service) ReapplyApp(ctx context.Context, appID int64) error {
	_ = appID // a configuração é sempre carregada inteira
	return s.Reapply(ctx)
}
