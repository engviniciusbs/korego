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

// Ensure garante que o korego-caddy existe e roda. As portas 80 e 443 são
// checadas quando o container não existe, quando está parado (antes do
// Start) e quando roda com config vazia: nesses casos o Caddy não as segura,
// então qualquer escuta nelas é de outro processo. Com config carregada,
// quem as ocupa é o próprio Caddy. Só usa um korego-caddy com o label
// korego.managed=true.
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
		if !ct.Managed {
			// Mesmo nome, mas não é nosso: não usa, não inicia, não para.
			return Status{State: StateError, Message: "Já existe um container korego-caddy que não foi criado pelo KoreGO."}
		}
		switch ct.State {
		case "running", "restarting":
		default:
			// Parado, o container não segura nenhuma porta: qualquer dono é alheio.
			if st, blocked := c.portsBlocked(); blocked {
				return st
			}
			if err := c.docker.Start(ctx, ct.ID); err != nil {
				return failure("iniciar "+ContainerName, err)
			}
		}
		return c.ping(ctx)
	}

	if st, blocked := c.portsBlocked(); blocked {
		return st
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

// portsBlocked confere 80 e 443; ocupada → blocked com o nome do processo.
func (c *Caddy) portsBlocked() (Status, bool) {
	for _, port := range []int{80, 443} {
		if owner, busy := c.PortOwner(port); busy {
			return Status{
				State:   StateBlocked,
				Process: owner,
				Message: "Proxy inativo: as portas 80/443 estão em uso por " + owner,
			}, true
		}
	}
	return Status{}, false
}

// ping devolve starting enquanto a Admin API não responde. Respondendo com
// config vazia ("null"), o Caddy ainda não escuta em 80/443, então qualquer
// escuta nelas é de outro processo → blocked (o container fica como está).
// Com config carregada, o Caddy é quem segura as portas → active.
func (c *Caddy) ping(ctx context.Context) Status {
	pctx, cancel := context.WithTimeout(ctx, adminPingTimeout)
	defer cancel()
	cfg, err := c.admin.Config(pctx)
	if err != nil {
		return Status{State: StateStarting, Message: "aguardando a Admin API do Caddy"}
	}
	if SameConfig(cfg, nil) {
		if st, blocked := c.portsBlocked(); blocked {
			return st
		}
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
