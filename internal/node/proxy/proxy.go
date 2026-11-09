// Package proxy controla o proxy reverso do KoreGO (Caddy).
//
// Este arquivo define os sites que o pacote recebe e a interface Proxy.
// caddyconfig.go transforma os sites na configuração JSON nativa do Caddy,
// admin.go conversa com a Admin API (127.0.0.1:2019) para aplicá-la e
// caddy.go gerencia o container korego-caddy.
//
// O pacote não importa outros pacotes internos (exceto node/docker).
package proxy

import (
	"context"
	"errors"
	"time"
)

// Valores aceitos em Site.WWWRedirect.
const (
	WWWNone      = "none"
	WWWToApex    = "www_to_apex"
	WWWApexToWWW = "apex_to_www"
)

// Listen é o modo de escuta do Caddy.
type Listen string

// Modos de escuta: normal = :80 e :443; trial (ensaio) = 127.0.0.1:18080 e
// 127.0.0.1:18443.
const (
	ListenNormal Listen = "normal"
	ListenTrial  Listen = "trial"
)

// Modos de SiteTLS.
const (
	TLSAuto   = "auto"
	TLSCustom = "custom"
)

// Site é um domínio publicado pelo proxy, com as suas rotas por caminho.
type Site struct {
	Host        string // hostname principal
	WWWRedirect string // none | www_to_apex | apex_to_www ("" = none)
	ForceHTTPS  bool
	Routes      []SiteRoute // 1..20; a rota "/" é obrigatória
	TLS         SiteTLS
	// CloudflareOnly recusa (403) quem não vem das faixas da Cloudflare.
	CloudflareOnly bool
	Headers        []Header // cabeçalhos de resposta definidos no domínio
	Compression    bool     // encode com zstd e gzip
}

// SkippedSite é um domínio gravado que ficou fora da configuração do proxy,
// com o motivo (certificado ruim, app sem porta, host já usado, ...).
type SkippedSite struct {
	Host   string `json:"host"`
	Reason string `json:"reason"`
}

// SiteRoute é uma rota de caminho dentro de um Site. Exatamente um entre
// Upstream e Static vem preenchido.
type SiteRoute struct {
	Path         string // "/" ou "/x/" (começa e termina com "/")
	Upstream     string // "127.0.0.1:20001", para alvos app e endereço
	MaxBodyBytes int64  // 0 = sem limite; acima do limite, 413
	NoBuffering  bool   // flush_interval -1
	Static       *StaticTarget
}

// StaticTarget é uma pasta servida diretamente pelo Caddy.
type StaticTarget struct {
	Root            string // absoluta, dentro de /var/www
	SPA             bool   // try_files {path} /index.html
	ImmutablePrefix string // caminho com cache imutável de um ano
}

// SiteTLS escolhe a origem do certificado do Site.
type SiteTLS struct {
	Mode    string // auto | custom ("" = auto)
	CertPEM string // cadeia + chave, só quando custom
}

// Header é um cabeçalho de resposta.
type Header struct {
	Name, Value string
}

// State é o estado do proxy na máquina.
type State string

// Estados possíveis do proxy.
const (
	StateActive            State = "active"
	StateStarting          State = "starting"
	StateBlocked           State = "blocked"
	StateTrial             State = "trial" // portas 80/443 de outro processo: Caddy roda em modo ensaio
	StateDockerUnavailable State = "docker_unavailable"
	StateError             State = "error"
)

// Status é o resultado de Proxy.Ensure.
type Status struct {
	State   State  `json:"state"`
	Mode    Listen `json:"mode,omitempty"`    // normal | trial, quando o proxy roda
	Process string `json:"process,omitempty"` // "nginx (pid 1402285)" quando blocked ou trial
	Message string `json:"message,omitempty"`
}

// Estados de CertInfo.
const (
	CertPending = "pending"
	CertActive  = "active"
	CertError   = "error"
)

// CertInfo é o estado do certificado servido para um host.
type CertInfo struct {
	State    string    `json:"state"` // pending | active | error
	NotAfter time.Time `json:"notAfter,omitempty"`
	Issuer   string    `json:"issuer,omitempty"`
	Message  string    `json:"message,omitempty"`
}

// Proxy é o proxy reverso de um node.
type Proxy interface {
	// Ensure sobe o korego-caddy se possível; nunca mexe em processo alheio.
	Ensure(ctx context.Context) Status
	// Apply carrega a configuração inteira; ErrInactive se o último Ensure
	// não terminou em active. cf são as faixas da Cloudflare (CIDRs).
	Apply(ctx context.Context, sites []Site, mode Listen, cf []string) error
	// InSync diz se a config atual do Caddy é igual a
	// BuildConfig(sites, mode, cf).
	InSync(ctx context.Context, sites []Site, mode Listen, cf []string) (bool, error)
	// Switch carrega a config no modo pedido ignorando os guardas de Apply e
	// alinha o último estado ao modo. Uso exclusivo do motor de migração.
	Switch(ctx context.Context, sites []Site, mode Listen, cf []string) error
	// Cert consulta o certificado servido em :443 para host.
	Cert(ctx context.Context, host string) CertInfo
}

// ErrInactive indica que o proxy não está ativo.
var ErrInactive = errors.New("proxy inativo")

// ErrTrialMode indica que o modo normal foi recusado porque o proxy está em
// ensaio (outro processo segura 80/443).
var ErrTrialMode = errors.New("proxy em modo ensaio: o modo normal não pode ser aplicado")
