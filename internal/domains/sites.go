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

// errNoRootUpstream indica que a rota "/" aponta para uma app sem porta (ainda
// sem deploy): o domínio fica de fora sem log.
var errNoRootUpstream = errors.New("rota / sem porta")

// siteOf converte um domínio gravado em proxy.Site, sem o PEM do certificado
// próprio. appUpstream devolve o destino de uma app ("" = sem porta). Rotas
// app sem porta ficam de fora; se for a rota "/", devolve errNoRootUpstream.
func siteOf(d store.Domain, appUpstream func(appID int64) string) (proxy.Site, error) {
	site := proxy.Site{
		Host: d.Hostname, ForceHTTPS: d.ForceHTTPS, WWWRedirect: wwwMode(d.WWWRedirect),
		TLS:            proxy.SiteTLS{Mode: d.TLSMode},
		CloudflareOnly: d.CloudflareOnly, Compression: d.Compression,
	}
	if site.TLS.Mode == "" {
		site.TLS.Mode = proxy.TLSAuto
	}
	for _, r := range d.Routes {
		sr := proxy.SiteRoute{Path: r.Path}
		switch r.TargetType {
		case TargetApp:
			if r.AppID != nil {
				sr.Upstream = appUpstream(*r.AppID)
			}
			if sr.Upstream == "" {
				if r.Path == "/" {
					return site, errNoRootUpstream
				}
				continue
			}
			sr.MaxBodyBytes, sr.NoBuffering = r.MaxBodyBytes, r.NoBuffering
		case TargetAddress:
			sr.Upstream = r.Address
			sr.MaxBodyBytes, sr.NoBuffering = r.MaxBodyBytes, r.NoBuffering
		case TargetStatic:
			sr.Static = &proxy.StaticTarget{Root: r.Root, SPA: r.SPA, ImmutablePrefix: r.ImmutablePrefix}
		default:
			return site, fmt.Errorf("rota %s com destino desconhecido %q", r.Path, r.TargetType)
		}
		site.Routes = append(site.Routes, sr)
	}
	for _, h := range d.Headers {
		site.Headers = append(site.Headers, proxy.Header{Name: h.Name, Value: h.Value})
	}
	return site, nil
}

// cfRanges devolve as faixas da Cloudflare atuais (nil sem Ranges).
func (s *Service) cfRanges() []string {
	if s.Ranges == nil {
		return nil
	}
	return s.Ranges.Get()
}

// certPEM decifra o certificado próprio do domínio.
func (s *Service) certPEM(ctx context.Context, d store.Domain) (string, error) {
	if d.CertID == nil {
		return "", errors.New("certificado próprio sem certificado escolhido")
	}
	if s.Certs == nil {
		return "", errors.New("serviço de certificados indisponível")
	}
	return s.Certs.PEM(ctx, *d.CertID)
}

// panelRoutes é a rota fixa do domínio do painel.
func (s *Service) panelRoutes() []store.DomainRoute {
	return []store.DomainRoute{{Path: "/", TargetType: TargetAddress, Address: localUpstream(s.PanelPort)}}
}

// Sites monta os sites do proxy a partir dos domínios gravados:
//   - painel → uma rota "/" para 127.0.0.1:<PanelPort> (fica de fora se
//     PanelPort ≤ 0), com as opções gravadas (TLS, só Cloudflare, ...);
//   - rota app → 127.0.0.1:<host_port>, só com host_port > 0 e app existente
//     (rota "/" sem porta deixa o domínio de fora);
//   - rota address → como está; rota static → pasta em /var/www;
//   - certificado próprio → PEM decifrado (falhou, o domínio fica de fora).
//
// Uma linha ruim não pode derrubar os demais (BuildConfig recusa o conjunto
// inteiro): cada site precisa passar sozinho por proxy.BuildConfig, e linhas
// com hostname inválido, redirecionamento www que não combina com o hostname
// ou host (ou par www) já tomado por outra linha ficam de fora e vão para o
// log. O domínio do painel reserva os seus hosts primeiro; depois vale a
// ordem por hostname. A saída segue por hostname.
func (s *Service) Sites(ctx context.Context) ([]proxy.Site, error) {
	list, err := s.DB.ListDomains(ctx)
	if err != nil {
		return nil, fmt.Errorf("montar sites: %w", err)
	}
	apps, err := s.appsByID(ctx)
	if err != nil {
		return nil, fmt.Errorf("montar sites: %w", err)
	}
	appUpstream := func(id int64) string {
		if a, ok := apps[id]; ok && a.HostPort > 0 {
			return localUpstream(a.HostPort)
		}
		return ""
	}

	type cand struct {
		d    store.Domain
		site proxy.Site
	}
	cands := make([]cand, 0, len(list))
	for _, d := range list {
		if d.IsPanel {
			if s.PanelPort <= 0 {
				continue
			}
			d.Routes = s.panelRoutes()
		}
		site, err := siteOf(d, appUpstream)
		if errors.Is(err, errNoRootUpstream) {
			continue
		}
		if err != nil {
			log.Printf("proxy: domínio %s (id %d) ignorado: %v", d.Hostname, d.ID, err)
			continue
		}
		cands = append(cands, cand{d, site})
	}

	// Reserva de hosts: painel primeiro, depois a ordem por hostname.
	order := make([]int, len(cands))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return cands[order[a]].d.IsPanel && !cands[order[b]].d.IsPanel
	})
	cf := s.cfRanges()
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
		if cands[i].site.TLS.Mode == proxy.TLSCustom {
			pemText, err := s.certPEM(ctx, d)
			if err != nil {
				log.Printf("proxy: domínio %s (id %d) ignorado: certificado próprio: %v", d.Hostname, d.ID, err)
				continue
			}
			cands[i].site.TLS.CertPEM = pemText
		}
		if _, err := proxy.BuildConfig([]proxy.Site{cands[i].site}, proxy.ListenNormal, cf); err != nil {
			log.Printf("proxy: domínio %s (id %d) ignorado: %v", d.Hostname, d.ID, err)
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
			sites = append(sites, c.site)
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

// Reapply monta os sites e os carrega no proxy, no modo de escuta do último
// status visto e com as faixas da Cloudflare atuais. Devolve
// proxy.ErrInactive se o proxy não estiver rodando.
func (s *Service) Reapply(ctx context.Context) error {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	sites, err := s.Sites(ctx)
	if err != nil {
		return err
	}
	cf := s.cfRanges()
	mode := s.Status().Mode
	if mode == "" {
		mode = proxy.ListenNormal
	}
	err = s.Proxy.Apply(ctx, sites, mode, cf)
	if errors.Is(err, proxy.ErrTrialMode) {
		// O status visto estava velho: o proxy está no outro modo. Uma única
		// tentativa, sem laço.
		other := proxy.ListenTrial
		if mode == proxy.ListenTrial {
			other = proxy.ListenNormal
		}
		err = s.Proxy.Apply(ctx, sites, other, cf)
	}
	return err
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
