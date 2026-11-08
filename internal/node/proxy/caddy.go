package proxy

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/engviniciusbs/korego/internal/node/docker"
)

const (
	// CaddyImage é a imagem do Caddy usada pelo KoreGO (versão fixa).
	CaddyImage = "caddy:2.11.9-alpine"
	// ContainerName é o nome do container do proxy.
	ContainerName = "korego-caddy"

	caddyRepo        = "caddy"
	defaultCertAddr  = "127.0.0.1:443"
	defaultCertWait  = 3 * time.Second
	adminPingTimeout = 2 * time.Second
)

// Caddy implementa Proxy com um container korego-caddy em rede host.
type Caddy struct {
	docker docker.Client
	admin  *AdminClient

	// PortOwner informa quem escuta numa porta (padrão: PortOwner do pacote).
	PortOwner func(port int) (string, bool)
	// CertAddr é onde Cert faz o dial TLS (padrão "127.0.0.1:443").
	CertAddr string
	// CertRoots são as raízes aceitas por Cert (nil = raízes do sistema).
	CertRoots *x509.CertPool
	// CertTimeout limita o dial e o handshake de Cert (padrão 3 s).
	CertTimeout time.Duration

	mu   sync.Mutex
	last Status
}

var _ Proxy = (*Caddy)(nil)

// NewCaddy cria o proxy Caddy sobre o cliente Docker e a Admin API dados.
func NewCaddy(d docker.Client, admin *AdminClient) *Caddy {
	return &Caddy{
		docker:      d,
		admin:       admin,
		PortOwner:   PortOwner,
		CertAddr:    defaultCertAddr,
		CertTimeout: defaultCertWait,
	}
}

// Ensure garante que o korego-caddy existe e roda. Só checa as portas 80 e
// 443 quando o container ainda não existe: com ele de pé, quem as ocupa é o
// próprio Caddy.
func (c *Caddy) Ensure(ctx context.Context) Status {
	st := c.ensure(ctx)
	c.mu.Lock()
	c.last = st
	c.mu.Unlock()
	return st
}

func (c *Caddy) ensure(ctx context.Context) Status {
	list, err := c.docker.List(ctx)
	if err != nil {
		return failure("listar containers", err)
	}
	for _, ct := range list {
		if ct.Name != ContainerName {
			continue
		}
		switch ct.State {
		case "running", "restarting":
		default:
			if err := c.docker.Start(ctx, ct.ID); err != nil {
				return failure("iniciar "+ContainerName, err)
			}
		}
		return c.ping(ctx)
	}

	for _, port := range []int{80, 443} {
		if owner, busy := c.PortOwner(port); busy {
			return Status{
				State:   StateBlocked,
				Process: owner,
				Message: fmt.Sprintf("a porta %d está ocupada por %s", port, owner),
			}
		}
	}
	if err := c.ensureImage(ctx); err != nil {
		return failure("baixar "+CaddyImage, err)
	}
	_, err = c.docker.Run(ctx, docker.RunSpec{
		Name:        ContainerName,
		Image:       CaddyImage,
		Labels:      map[string]string{"korego.managed": "true"},
		Cmd:         []string{"caddy", "run"},
		NetworkMode: "host",
		Volumes: []docker.VolumeMount{
			{Volume: "korego-caddy-data", Target: "/data"},
			{Volume: "korego-caddy-config", Target: "/config"},
		},
		RestartPolicy: "unless-stopped",
	})
	// ErrConflict: outro Ensure criou o container ao mesmo tempo.
	if err != nil && !errors.Is(err, docker.ErrConflict) {
		return failure("criar "+ContainerName, err)
	}
	return c.ping(ctx)
}

// ensureImage baixa CaddyImage se ela não está no Docker local.
func (c *Caddy) ensureImage(ctx context.Context) error {
	imgs, err := c.docker.Images(ctx, caddyRepo)
	if err != nil {
		return err
	}
	for _, img := range imgs {
		if img.Ref == CaddyImage {
			return nil
		}
	}
	return c.docker.Pull(ctx, CaddyImage, nil)
}

// ping devolve active se a Admin API responde, senão starting.
func (c *Caddy) ping(ctx context.Context) Status {
	pctx, cancel := context.WithTimeout(ctx, adminPingTimeout)
	defer cancel()
	if err := c.admin.Ping(pctx); err != nil {
		return Status{State: StateStarting, Message: "aguardando a Admin API do Caddy"}
	}
	return Status{State: StateActive}
}

func failure(what string, err error) Status {
	if errors.Is(err, docker.ErrUnavailable) {
		return Status{State: StateDockerUnavailable, Message: "Docker indisponível"}
	}
	return Status{State: StateError, Message: fmt.Sprintf("%s: %v", what, err)}
}

// Apply substitui a configuração do Caddy pela gerada para routes.
func (c *Caddy) Apply(ctx context.Context, routes []Route) error {
	c.mu.Lock()
	state := c.last.State
	c.mu.Unlock()
	if state != StateActive {
		return ErrInactive
	}
	cfg, err := BuildConfig(routes)
	if err != nil {
		return err
	}
	return c.admin.Load(ctx, cfg)
}

// InSync compara a configuração atual do Caddy com BuildConfig(routes). Um
// Caddy recém-reiniciado (config "null") nunca está em sincronia.
func (c *Caddy) InSync(ctx context.Context, routes []Route) (bool, error) {
	want, err := BuildConfig(routes)
	if err != nil {
		return false, err
	}
	cur, err := c.admin.Config(ctx)
	if err != nil {
		return false, err
	}
	return SameConfig(cur, want), nil
}
