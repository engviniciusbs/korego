package domains

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sort"
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

// Routes monta os sites do proxy a partir dos domínios gravados, cada um com
// uma única rota "/" (as rotas por caminho entram na Task 6):
//   - painel → 127.0.0.1:<PanelPort> (fica de fora se PanelPort ≤ 0);
//   - app → 127.0.0.1:<host_port>, só com host_port > 0 e app existente;
//   - endereço manual → como está.
//
// Uma linha ruim não pode derrubar todas as rotas (BuildConfig recusa o
// conjunto inteiro): linhas com hostname inválido, redirecionamento www que
// não combina com o hostname ou host (ou par www) já tomado por outra linha
// ficam de fora e vão para o log. O domínio do painel reserva os seus hosts
// primeiro; depois vale a ordem por hostname. A saída segue por hostname.
func (s *Service) Routes(ctx context.Context) ([]proxy.Site, error) {
	list, err := s.DB.ListDomains(ctx)
	if err != nil {
		return nil, fmt.Errorf("montar rotas: %w", err)
	}
	apps, err := s.appsByID(ctx)
	if err != nil {
		return nil, fmt.Errorf("montar rotas: %w", err)
	}

	type cand struct {
		d        store.Domain
		upstream string
	}
	cands := make([]cand, 0, len(list))
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
		if upstream != "" {
			cands = append(cands, cand{d, upstream})
		}
	}

	// Reserva de hosts: painel primeiro, depois a ordem por hostname.
	order := make([]int, len(cands))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return cands[order[a]].d.IsPanel && !cands[order[b]].d.IsPanel
	})
	taken := map[string]string{} // host → hostname da linha que o reservou
	accepted := make([]bool, len(cands))
	for _, i := range order {
		d := cands[i].d
		if !ValidHostname(d.Hostname) {
			log.Printf("proxy: domínio %q ignorado: hostname inválido", d.Hostname)
			continue
		}
		if msg := wwwMismatch(d.Hostname, d.WWWRedirect); msg != "" {
			log.Printf("proxy: domínio %s ignorado: %s", d.Hostname, msg)
			continue
		}
		hosts := hostsOf(d.Hostname, d.WWWRedirect)
		clash := ""
		for _, h := range hosts {
			if owner, ok := taken[h]; ok {
				clash = fmt.Sprintf("o host %s já é usado pelo domínio %s", h, owner)
				break
			}
		}
		if clash != "" {
			log.Printf("proxy: domínio %s (id %d) ignorado: %s", d.Hostname, d.ID, clash)
			continue
		}
		for _, h := range hosts {
			taken[h] = d.Hostname
		}
		accepted[i] = true
	}

	sites := []proxy.Site{}
	for i, c := range cands {
		if accepted[i] {
			sites = append(sites, proxy.Site{Host: c.d.Hostname,
				ForceHTTPS: c.d.ForceHTTPS, WWWRedirect: wwwMode(c.d.WWWRedirect),
				Routes: []proxy.SiteRoute{{Path: "/", Upstream: c.upstream}}})
		}
	}
	return sites, nil
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
	return s.Proxy.Apply(ctx, routes, proxy.ListenNormal, nil)
}

// ReapplyApp é chamado pelo deploy depois de trocar a porta da app.
//
// Com o último status visto pela reconciliação em blocked (outro processo,
// como o nginx, segura 80/443 e o Caddy não segura porta nenhuma), devolve
// nil: a rota é aplicada pela reconciliação quando o proxy ficar ativo. Nos
// demais estados inativos (starting, docker_unavailable, error) segue
// devolvendo proxy.ErrInactive.
func (s *Service) ReapplyApp(ctx context.Context, appID int64) error {
	_ = appID // a configuração é sempre carregada inteira
	err := s.Reapply(ctx)
	if errors.Is(err, proxy.ErrInactive) && s.Status().State == proxy.StateBlocked {
		s.statusMu.Lock()
		first := !s.blockedLogged
		s.blockedLogged = true
		s.statusMu.Unlock()
		if first {
			log.Printf("proxy bloqueado; rota aplicada quando o proxy ficar ativo")
		}
		return nil
	}
	return err
}
