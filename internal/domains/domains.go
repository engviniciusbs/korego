// Package domains é o serviço de domínios do KoreGO: valida e grava os
// domínios, verifica o DNS, monta os sites do proxy e mantém o Caddy em
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

	"github.com/engviniciusbs/korego/internal/certs"
	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/store"
)

// Tipos de destino de uma rota.
const (
	TargetApp     = "app"
	TargetAddress = "address"
	TargetStatic  = "static"
)

// Códigos de erro de validação (ErrInvalid.Code).
const (
	CodeInvalidDomain = "INVALID_DOMAIN"
	CodeInvalidRoute  = "INVALID_ROUTE"
	CodeInvalidPath   = "INVALID_PATH"
	CodeInvalidRoot   = "INVALID_ROOT"
	CodeInvalidHeader = "INVALID_HEADER"
	CodeInvalidCert   = "INVALID_CERT"
	CodeCertMismatch  = "CERT_MISMATCH"
	CodeCertExpired   = "CERT_EXPIRED"
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
	Code    string // Code* (INVALID_DOMAIN, INVALID_ROUTE, ...)
	Field   string // hostname | targetType | routes[0].path | headers[1].name | tls.certId | ...
	Message string
}

func (e *ErrInvalid) Error() string { return e.Message }

func invalid(field, msg string) error {
	return &ErrInvalid{Code: CodeInvalidDomain, Field: field, Message: msg}
}

func invalidCode(code, field, msg string) error {
	return &ErrInvalid{Code: code, Field: field, Message: msg}
}

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
	// Certs decifra os certificados próprios (nil = domínios com certificado
	// próprio são recusados e ficam fora do proxy).
	Certs *certs.Service
	// Ranges devolve as faixas da Cloudflare passadas ao proxy (nil = nenhuma).
	Ranges interface{ Get() []string }

	panelMu   sync.Mutex // serializa as alterações do painel
	applyMu   sync.Mutex // serializa Sites+Apply
	statusMu  sync.Mutex
	status    proxy.Status
	hasStatus bool
	// blockedLogged evita repetir o log de ReapplyApp com o proxy bloqueado;
	// volta a false quando o status sai de blocked.
	blockedLogged bool
}

// DomainInput são os campos editáveis de um domínio.
//
// Sem Routes, TargetType, AppID e Address viram uma única rota "/" (corpo
// antigo). Com Routes, esses três campos são ignorados.
type DomainInput struct {
	Hostname       string         `json:"hostname"`
	TargetType     string         `json:"targetType"`
	AppID          *int64         `json:"appId"`
	Address        string         `json:"address"`
	WWWRedirect    string         `json:"wwwRedirect"`
	ForceHTTPS     bool           `json:"forceHttps"`
	Routes         []RouteInput   `json:"routes"`
	TLS            TLSInput       `json:"tls"`
	CloudflareOnly bool           `json:"cloudflareOnly"`
	Headers        []store.Header `json:"headers"`
	// Compression: nil = ligada na criação e mantida na edição.
	Compression *bool `json:"compression"`
}

// RouteInput é uma rota por caminho de um domínio.
type RouteInput struct {
	Path            string `json:"path"`
	TargetType      string `json:"targetType"` // app | address | static
	AppID           *int64 `json:"appId"`
	Address         string `json:"address"`
	MaxBodyBytes    int64  `json:"maxBodyBytes"` // 0 = sem limite
	NoBuffering     bool   `json:"noBuffering"`
	Root            string `json:"root"`
	SPA             bool   `json:"spa"`
	ImmutablePrefix string `json:"immutablePrefix"`
}

// TLSInput escolhe o certificado do domínio.
type TLSInput struct {
	Mode   string `json:"mode"` // auto | custom ("" = auto)
	CertID *int64 `json:"certId"`
}

// TLSView é o certificado escolhido para o domínio, para a API.
type TLSView struct {
	Mode        string     `json:"mode"`
	CertID      *int64     `json:"certId"`
	CertName    string     `json:"certName,omitempty"`
	NotAfter    *time.Time `json:"notAfter,omitempty"`
	ExpiresSoon bool       `json:"expiresSoon"`
}

// DomainView é o domínio com o estado do certificado, o slug da app da rota
// "/" e o certificado escolhido.
type DomainView struct {
	store.Domain
	Cert    proxy.CertInfo `json:"cert"`
	AppSlug string         `json:"appSlug,omitempty"`
	TLS     TLSView        `json:"tls"`
}

// validate confere a entrada e devolve o domínio normalizado (sem ID e sem
// Compression). excludeID é o domínio sendo editado (0 na criação).
func (s *Service) validate(ctx context.Context, in DomainInput, excludeID int64) (store.Domain, error) {
	d := store.Domain{
		Hostname:    strings.TrimSpace(in.Hostname),
		WWWRedirect: in.WWWRedirect,
		ForceHTTPS:  in.ForceHTTPS,
	}
	if !ValidHostname(d.Hostname) {
		return d, invalid("hostname", "Hostname inválido. Use letras minúsculas, números e hífens, como app.exemplo.com.")
	}
	var err error
	if in.Routes == nil {
		d.Routes, err = s.legacyRoute(ctx, in)
	} else {
		d.Routes, err = s.validRoutes(ctx, in.Routes)
	}
	if err != nil {
		return d, err
	}
	d.TargetType, d.AppID, d.Address = rootTarget(d.Routes)
	if d.WWWRedirect == "" {
		d.WWWRedirect = WWWNone
	}
	if msg := wwwMismatch(d.Hostname, d.WWWRedirect); msg != "" {
		return d, invalid("wwwRedirect", msg)
	}
	pemText, err := s.applyOptions(ctx, &d, in)
	if err != nil {
		return d, err
	}
	if err := s.CheckCollision(ctx, d.Hostname, d.WWWRedirect, excludeID); err != nil {
		return d, err
	}
	return d, s.buildCheck(d, pemText)
}

// rootTarget devolve o destino da rota "/" (os campos legados do domínio).
func rootTarget(routes []store.DomainRoute) (string, *int64, string) {
	for _, r := range routes {
		if r.Path == "/" {
			return r.TargetType, r.AppID, r.Address
		}
	}
	return "", nil, ""
}

// legacyRoute monta a rota "/" do corpo antigo (targetType, appId, address).
func (s *Service) legacyRoute(ctx context.Context, in DomainInput) ([]store.DomainRoute, error) {
	r := store.DomainRoute{Path: "/", TargetType: in.TargetType}
	switch in.TargetType {
	case TargetApp:
		if in.AppID == nil {
			return nil, invalid("appId", "Escolha a aplicação do domínio.")
		}
		if _, err := s.DB.GetApp(ctx, *in.AppID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, invalid("appId", "Aplicação não encontrada.")
			}
			return nil, err
		}
		id := *in.AppID
		r.AppID = &id
	case TargetAddress:
		r.Address = strings.TrimSpace(in.Address)
		if !ValidAddress(r.Address) {
			return nil, invalid("address", "Endereço inválido. Use host:porta, como 127.0.0.1:5000.")
		}
	default:
		return nil, invalid("targetType", "Destino inválido. Use app ou address.")
	}
	return []store.DomainRoute{r}, nil
}

// applyOptions valida e grava em d o certificado, o só Cloudflare e os
// cabeçalhos da entrada. Usa o hostname e o redirecionamento www já em d.
// Devolve o PEM do certificado próprio ("" = auto), para a conferência final.
func (s *Service) applyOptions(ctx context.Context, d *store.Domain, in DomainInput) (string, error) {
	headers, err := validHeaders(in.Headers)
	if err != nil {
		return "", err
	}
	mode, certID, pemText, err := s.validTLS(ctx, in.TLS, hostsOf(d.Hostname, d.WWWRedirect))
	if err != nil {
		return "", err
	}
	d.Headers, d.TLSMode, d.CertID, d.CloudflareOnly = headers, mode, certID, in.CloudflareOnly
	return pemText, nil
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
	d.Compression = in.Compression == nil || *in.Compression
	if err := s.DB.InsertDomain(ctx, &d); err != nil {
		return store.Domain{}, DNSCheck{}, err
	}
	s.reapplyAfterSave(ctx)
	return d, s.CheckDNS(ctx, d.Hostname), nil
}

// Update altera um domínio existente. No domínio do painel só valem as
// opções (ver updatePanel).
func (s *Service) Update(ctx context.Context, id int64, in DomainInput) (store.Domain, DNSCheck, error) {
	cur, err := s.DB.GetDomain(ctx, id)
	if err != nil {
		return store.Domain{}, DNSCheck{}, err
	}
	if cur.IsPanel {
		d, err := s.updatePanel(ctx, id, in)
		if err != nil {
			return store.Domain{}, DNSCheck{}, err
		}
		return d, s.CheckDNS(ctx, d.Hostname), nil
	}
	d, err := s.validate(ctx, in, id)
	if err != nil {
		return store.Domain{}, DNSCheck{}, err
	}
	d.ID, d.CreatedAt, d.IsPanel = id, cur.CreatedAt, cur.IsPanel
	d.Compression = cur.Compression
	if in.Compression != nil {
		d.Compression = *in.Compression
	}
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
	certList, err := s.DB.ListCertificates(ctx)
	if err != nil {
		return nil, fmt.Errorf("listar domínios: %w", err)
	}
	certsByID := make(map[int64]store.Certificate, len(certList))
	for _, c := range certList {
		certsByID[c.ID] = c
	}
	out := make([]DomainView, len(list))
	sem := make(chan struct{}, certConcurrency)
	var wg sync.WaitGroup
	for i, d := range list {
		out[i] = s.viewOf(d, apps, certsByID)
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

// View devolve um domínio com o certificado servido, o slug da app e o
// certificado escolhido.
func (s *Service) View(ctx context.Context, d store.Domain) (DomainView, error) {
	apps, err := s.appsByID(ctx)
	if err != nil {
		return DomainView{}, err
	}
	certsByID := map[int64]store.Certificate{}
	if d.CertID != nil {
		c, err := s.DB.CertificateByID(ctx, *d.CertID)
		switch {
		case err == nil:
			certsByID[c.ID] = c
		case !errors.Is(err, store.ErrNotFound):
			return DomainView{}, err
		}
	}
	v := s.viewOf(d, apps, certsByID)
	v.Cert = s.Proxy.Cert(ctx, d.Hostname)
	return v, nil
}

// viewOf monta a DomainView, sem o estado do certificado servido.
func (s *Service) viewOf(d store.Domain, apps map[int64]store.App, certsByID map[int64]store.Certificate) DomainView {
	v := DomainView{Domain: d, TLS: TLSView{Mode: d.TLSMode}}
	if v.TLS.Mode == "" {
		v.TLS.Mode = proxy.TLSAuto
	}
	if d.AppID != nil {
		v.AppSlug = apps[*d.AppID].Slug
	}
	if d.TLSMode == proxy.TLSCustom && d.CertID != nil {
		id := *d.CertID
		v.TLS.CertID = &id
		if c, ok := certsByID[id]; ok {
			na := c.NotAfter
			v.TLS.CertName, v.TLS.NotAfter = c.Name, &na
			v.TLS.ExpiresSoon = c.NotAfter.Sub(s.now()) <= certs.ExpiresSoonWindow
		}
	}
	return v
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
