// Package domains é o serviço de domínios do KoreGO: valida e grava os
// domínios, verifica o DNS, monta as rotas do proxy e mantém o Caddy em
// sincronia com o banco (Run).
package domains

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/store"
)

// Tipos de destino de um domínio.
const (
	TargetApp     = "app"
	TargetAddress = "address"
)

// Valores de redirecionamento www gravados no banco.
const (
	WWWNone      = "none"
	WWWToApex    = "www_to_apex"
	WWWApexToWWW = "apex_to_www"
)

// certConcurrency limita as consultas de certificado simultâneas em List.
const certConcurrency = 4

var (
	// ErrNotFound indica domínio inexistente.
	ErrNotFound = store.ErrNotFound
	// ErrConflict indica hostname já cadastrado.
	ErrConflict = store.ErrConflict
	// ErrPanelDomain indica tentativa de alterar o domínio do painel por aqui.
	ErrPanelDomain = errors.New("o domínio do painel só pode ser alterado nas configurações do painel")
)

// ErrInvalid é um dado de entrada inválido. Use errors.As com *ErrInvalid.
type ErrInvalid struct {
	Field   string // hostname | targetType | appId | address | wwwRedirect
	Message string
}

func (e *ErrInvalid) Error() string { return e.Message }

func invalid(field, msg string) error { return &ErrInvalid{Field: field, Message: msg} }

// Service é o serviço de domínios.
type Service struct {
	DB       *store.DB
	Proxy    proxy.Proxy
	Hub      *events.Hub
	Resolver Resolver // nil = net.DefaultResolver (rede "ip")
	// HostIPs devolve os IPs desta máquina (nil = InterfaceIPs). PublicIP
	// é somado aos IPs esperados na verificação de DNS.
	HostIPs  func() ([]net.IP, error)
	PublicIP string
	Now      func() time.Time
	Interval time.Duration // 0 = 30 s
	// PanelPort é a porta do painel; a rota do domínio do painel vai para
	// 127.0.0.1:<PanelPort>.
	PanelPort int
	// ListenAddr é o endereço de escuta configurado (cfg.ListenAddr), usado
	// com a porta pública ligada; desligada, vale 127.0.0.1:<porta>.
	ListenAddr string
	// OnListenChange troca o endereço de escuta do painel (nil = nada).
	OnListenChange func(addr string) error

	panelMu   sync.Mutex // serializa as alterações do painel
	applyMu   sync.Mutex // serializa Routes+Apply
	statusMu  sync.Mutex
	status    proxy.Status
	hasStatus bool
}

// DomainInput são os campos editáveis de um domínio.
type DomainInput struct {
	Hostname    string `json:"hostname"`
	TargetType  string `json:"targetType"`
	AppID       *int64 `json:"appId"`
	Address     string `json:"address"`
	WWWRedirect string `json:"wwwRedirect"`
	ForceHTTPS  bool   `json:"forceHttps"`
}

// DomainView é o domínio com o estado do certificado e o slug da app.
type DomainView struct {
	store.Domain
	Cert    proxy.CertInfo `json:"cert"`
	AppSlug string         `json:"appSlug,omitempty"`
}

// validate confere a entrada e devolve o domínio normalizado (sem ID).
// excludeID é o domínio sendo editado (0 na criação).
func (s *Service) validate(ctx context.Context, in DomainInput, excludeID int64) (store.Domain, error) {
	d := store.Domain{
		Hostname:    strings.TrimSpace(in.Hostname),
		TargetType:  in.TargetType,
		WWWRedirect: in.WWWRedirect,
		ForceHTTPS:  in.ForceHTTPS,
	}
	if !ValidHostname(d.Hostname) {
		return d, invalid("hostname", "Hostname inválido. Use letras minúsculas, números e hífens, como app.exemplo.com.")
	}
	switch d.TargetType {
	case TargetApp:
		if in.AppID == nil {
			return d, invalid("appId", "Escolha a aplicação do domínio.")
		}
		if _, err := s.DB.GetApp(ctx, *in.AppID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return d, invalid("appId", "Aplicação não encontrada.")
			}
			return d, err
		}
		id := *in.AppID
		d.AppID = &id
	case TargetAddress:
		d.Address = strings.TrimSpace(in.Address)
		if !ValidAddress(d.Address) {
			return d, invalid("address", "Endereço inválido. Use host:porta, como 127.0.0.1:5000.")
		}
	default:
		return d, invalid("targetType", "Destino inválido. Use app ou address.")
	}
	if d.WWWRedirect == "" {
		d.WWWRedirect = WWWNone
	}
	if msg := wwwMismatch(d.Hostname, d.WWWRedirect); msg != "" {
		return d, invalid("wwwRedirect", msg)
	}
	if err := s.CheckCollision(ctx, d.Hostname, d.WWWRedirect, excludeID); err != nil {
		return d, err
	}
	return d, nil
}

// wwwMismatch devolve a mensagem de erro se o redirecionamento www não for
// conhecido ou não combinar com o hostname; "" se estiver certo.
func wwwMismatch(hostname, www string) string {
	switch www {
	case "", WWWNone:
		return ""
	case WWWToApex:
		if strings.HasPrefix(hostname, "www.") {
			return "Para redirecionar www para o domínio sem www, cadastre o domínio sem www."
		}
		return ""
	case WWWApexToWWW:
		if !strings.HasPrefix(hostname, "www.") {
			return "Para redirecionar para www, cadastre o domínio com www."
		}
		return ""
	default:
		return "Redirecionamento www inválido. Use none, www_to_apex ou apex_to_www."
	}
}

// hostsOf devolve o hostname e, com redirecionamento www, o seu par.
func hostsOf(hostname, www string) []string {
	switch www {
	case WWWToApex:
		return []string{hostname, "www." + hostname}
	case WWWApexToWWW:
		return []string{hostname, strings.TrimPrefix(hostname, "www.")}
	default:
		return []string{hostname}
	}
}

// CheckCollision recusa (ErrInvalid, campo hostname) um domínio cujo hostname
// ou par www coincida com o hostname ou o par www de outro domínio. exceptID é
// o domínio sendo editado (0 = nenhum). Hostname idêntico fica para o banco
// (ErrConflict).
func (s *Service) CheckCollision(ctx context.Context, hostname, www string, exceptID int64) error {
	list, err := s.DB.ListDomains(ctx)
	if err != nil {
		return err
	}
	mine := hostsOf(hostname, www)
	for _, o := range list {
		if o.ID == exceptID || o.Hostname == hostname {
			continue
		}
		for _, h := range hostsOf(o.Hostname, o.WWWRedirect) {
			for _, m := range mine {
				if h == m {
					return invalid("hostname", fmt.Sprintf("O hostname %s já é usado pelo domínio %s.", m, o.Hostname))
				}
			}
		}
	}
	return nil
}

// reapplyAfterSave recarrega o proxy depois de gravar. Falhas não desfazem a
// gravação: proxy inativo é esperado e os demais erros vão para o log (a
// reconciliação tenta de novo).
func (s *Service) reapplyAfterSave(ctx context.Context) {
	if err := s.Reapply(ctx); err != nil && !errors.Is(err, proxy.ErrInactive) {
		log.Printf("proxy: aplicar domínios: %v", err)
	}
}

// Create valida e grava um domínio, recarrega o proxy e verifica o DNS.
func (s *Service) Create(ctx context.Context, in DomainInput) (store.Domain, DNSCheck, error) {
	d, err := s.validate(ctx, in, 0)
	if err != nil {
		return store.Domain{}, DNSCheck{}, err
	}
	if err := s.DB.InsertDomain(ctx, &d); err != nil {
		return store.Domain{}, DNSCheck{}, err
	}
	s.reapplyAfterSave(ctx)
	return d, s.CheckDNS(ctx, d.Hostname), nil
}

// Update altera um domínio existente. Domínio do painel → ErrPanelDomain.
func (s *Service) Update(ctx context.Context, id int64, in DomainInput) (store.Domain, DNSCheck, error) {
	cur, err := s.DB.GetDomain(ctx, id)
	if err != nil {
		return store.Domain{}, DNSCheck{}, err
	}
	if cur.IsPanel {
		return store.Domain{}, DNSCheck{}, ErrPanelDomain
	}
	d, err := s.validate(ctx, in, id)
	if err != nil {
		return store.Domain{}, DNSCheck{}, err
	}
	d.ID, d.CreatedAt = id, cur.CreatedAt
	if err := s.DB.UpdateDomain(ctx, &d); err != nil {
		return store.Domain{}, DNSCheck{}, err
	}
	s.reapplyAfterSave(ctx)
	return d, s.CheckDNS(ctx, d.Hostname), nil
}

// Delete apaga um domínio. Domínio do painel → ErrPanelDomain.
func (s *Service) Delete(ctx context.Context, id int64) error {
	cur, err := s.DB.GetDomain(ctx, id)
	if err != nil {
		return err
	}
	if cur.IsPanel {
		return ErrPanelDomain
	}
	if err := s.DB.DeleteDomain(ctx, id); err != nil {
		return err
	}
	s.reapplyAfterSave(ctx)
	return nil
}

// List devolve os domínios com o certificado de cada um e o slug da app.
func (s *Service) List(ctx context.Context) ([]DomainView, error) {
	list, err := s.DB.ListDomains(ctx)
	if err != nil {
		return nil, err
	}
	apps, err := s.appsByID(ctx)
	if err != nil {
		return nil, fmt.Errorf("listar domínios: %w", err)
	}
	out := make([]DomainView, len(list))
	sem := make(chan struct{}, certConcurrency)
	var wg sync.WaitGroup
	for i, d := range list {
		out[i].Domain = d
		if d.AppID != nil {
			out[i].AppSlug = apps[*d.AppID].Slug
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out[i].Cert = s.Proxy.Cert(ctx, d.Hostname)
		}()
	}
	wg.Wait()
	return out, nil
}
