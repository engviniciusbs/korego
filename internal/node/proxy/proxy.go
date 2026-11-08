// Package proxy controla o proxy reverso do KoreGO (Caddy).
//
// Este arquivo define as rotas que o pacote recebe e a interface Proxy.
// caddyconfig.go transforma as rotas na configuração JSON nativa do Caddy,
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

// Valores aceitos em Route.WWWRedirect.
const (
	WWWNone      = "none"
	WWWToApex    = "www_to_apex"
	WWWApexToWWW = "apex_to_www"
)

// Route é um hostname publicado pelo proxy.
type Route struct {
	Host        string // hostname principal
	Upstream    string // "127.0.0.1:20001"
	ForceHTTPS  bool
	WWWRedirect string // none | www_to_apex | apex_to_www ("" = none)
}

// State é o estado do proxy na máquina.
type State string

// Estados possíveis do proxy.
const (
	StateActive            State = "active"
	StateStarting          State = "starting"
	StateBlocked           State = "blocked"
	StateDockerUnavailable State = "docker_unavailable"
	StateError             State = "error"
)

// Status é o resultado de Proxy.Ensure.
type Status struct {
	State   State  `json:"state"`
	Process string `json:"process,omitempty"` // "nginx (pid 1402285)" quando blocked
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
	// não terminou em active.
	Apply(ctx context.Context, routes []Route) error
	// InSync diz se a config atual do Caddy é igual a BuildConfig(routes).
	InSync(ctx context.Context, routes []Route) (bool, error)
	// Cert consulta o certificado servido em :443 para host.
	Cert(ctx context.Context, host string) CertInfo
}

// ErrInactive indica que o proxy não está ativo.
var ErrInactive = errors.New("proxy inativo")
