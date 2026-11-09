package proxy

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/engviniciusbs/korego/internal/node/docker"
)

const (
	// CaddyImage é a imagem do Caddy usada pelo KoreGO (versão fixa).
	CaddyImage = "caddy:2.11.9-alpine"
	// ContainerName é o nome do container do proxy.
	ContainerName = "korego-caddy"

	// CaddySpec é a versão do spec do container (label korego.caddy-spec).
	CaddySpec      = 2
	caddySpecLabel = "korego.caddy-spec"
	wwwDir         = "/var/www"

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
	// EnsureWWW cria /var/www no host se faltar (padrão: ensureWWWDir).
	EnsureWWW func() error

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
		EnsureWWW:   ensureWWWDir,
	}
}

// Ensure garante que o korego-caddy existe e roda, mesmo quando outro processo
// segura 80/443: o Caddy só escuta o que a config carregada manda, então com
// config vazia ou de ensaio ele não ocupa essas portas. Portas alheias dão o
// estado trial (Mode=trial, Process preenchido); livres, ou do próprio Caddy,
// dão active (Mode=normal). Só usa um korego-caddy com o label
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
		running := ct.State == "running" || ct.State == "restarting"
		if specVersion(ct.Labels) >= CaddySpec {
			return c.start(ctx, ct, running)
		}
		// Spec antiga: recria, mas só se o Caddy não está servindo 80/443.
		if running {
			if st, serving := c.probe(ctx); serving {
				log.Printf("proxy: recriação pendente (spec do %s desatualizada); Caddy está servindo", ContainerName)
				return st
			}
		}
		_ = c.docker.Stop(ctx, ct.ID) // pode já estar parado
		if err := c.docker.RemoveForce(ctx, ct.ID); err != nil {
			return failure("remover "+ContainerName, err)
		}
		break
	}
	return c.create(ctx)
}

// start usa o container existente, iniciando-o se estiver parado.
func (c *Caddy) start(ctx context.Context, ct docker.Container, running bool) Status {
	if !running {
		if err := c.docker.Start(ctx, ct.ID); err != nil {
			return failure("iniciar "+ContainerName, err)
		}
	}
	st, _ := c.probe(ctx)
	return st
}

func (c *Caddy) create(ctx context.Context) Status {
	if err := c.ensureImage(ctx); err != nil {
		return failure("baixar "+CaddyImage, err)
	}
	if c.EnsureWWW != nil {
		if err := c.EnsureWWW(); err != nil {
			log.Printf("proxy: criar %s: %v", wwwDir, err)
		}
	}
	_, err := c.docker.Run(ctx, docker.RunSpec{
		Name:  ContainerName,
		Image: CaddyImage,
		Labels: map[string]string{
			"korego.managed": "true",
			caddySpecLabel:   strconv.Itoa(CaddySpec),
		},
		Cmd:         []string{"caddy", "run"},
		NetworkMode: "host",
		Volumes: []docker.VolumeMount{
			{Volume: "korego-caddy-data", Target: "/data"},
			{Volume: "korego-caddy-config", Target: "/config"},
		},
		Binds:         []docker.BindMount{{Source: wwwDir, Target: wwwDir, ReadOnly: true}},
		RestartPolicy: "unless-stopped",
	})
	// ErrConflict: outro Ensure criou o container ao mesmo tempo.
	if err != nil && !errors.Is(err, docker.ErrConflict) {
		return failure("criar "+ContainerName, err)
	}
	st, _ := c.probe(ctx)
	return st
}

// specVersion lê o label da versão do spec; ausente ou inválido = 0.
func specVersion(labels map[string]string) int {
	n, err := strconv.Atoi(labels[caddySpecLabel])
	if err != nil {
		return 0
	}
	return n
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

// probe consulta a Admin API e as portas 80/443. Sem resposta → starting.
// serving diz se o Caddy está de fato servindo 80/443 (config escuta :443 e
// as portas são dele).
//
// Um processo alheio ocupa as portas quando o dono não é docker-proxy nem
// caddy, ou quando a config atual não escuta em :443 (config vazia ou de
// ensaio: o Caddy não segura 80/443, então qualquer dono é outro processo).
func (c *Caddy) probe(ctx context.Context) (st Status, serving bool) {
	pctx, cancel := context.WithTimeout(ctx, adminPingTimeout)
	defer cancel()
	cfg, err := c.admin.Config(pctx)
	if err != nil {
		return Status{State: StateStarting, Message: "aguardando a Admin API do Caddy"}, false
	}
	listens := listensOn443(cfg)
	for _, port := range []int{80, 443} {
		owner, busy := c.PortOwner(port)
		if busy && (!listens || !isCaddyOwner(owner)) {
			return Status{
				State:   StateTrial,
				Mode:    ListenTrial,
				Process: owner,
				Message: "Proxy em modo ensaio: as portas 80/443 estão em uso por " + owner,
			}, false
		}
	}
	return Status{State: StateActive, Mode: ListenNormal}, listens
}

// isCaddyOwner diz se o dono da porta ("nome (pid N)") é docker-proxy ou caddy.
func isCaddyOwner(owner string) bool {
	name, _, _ := strings.Cut(owner, " ")
	return name == "docker-proxy" || name == "caddy"
}

// listensOn443 diz se a config JSON do Caddy tem algum servidor escutando :443.
func listensOn443(cfg []byte) bool {
	var doc struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Listen []string `json:"listen"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if json.Unmarshal(cfg, &doc) != nil {
		return false
	}
	for _, srv := range doc.Apps.HTTP.Servers {
		if slices.Contains(srv.Listen, ":443") {
			return true
		}
	}
	return false
}

func failure(what string, err error) Status {
	if errors.Is(err, docker.ErrUnavailable) {
		return Status{State: StateDockerUnavailable, Message: "Docker indisponível"}
	}
	return Status{State: StateError, Message: fmt.Sprintf("%s: %v", what, err)}
}

// Apply substitui a configuração do Caddy pela gerada para os sites.
func (c *Caddy) Apply(ctx context.Context, sites []Site, mode Listen, cf []string) error {
	c.mu.Lock()
	state := c.last.State
	c.mu.Unlock()
	if state != StateActive && state != StateTrial {
		return ErrInactive
	}
	cfg, err := BuildConfig(sites, mode, cf)
	if err != nil {
		return err
	}
	return c.admin.Load(ctx, cfg)
}

// InSync compara a configuração atual do Caddy com BuildConfig(sites, mode, cf). Um
// Caddy recém-reiniciado (config "null") nunca está em sincronia.
func (c *Caddy) InSync(ctx context.Context, sites []Site, mode Listen, cf []string) (bool, error) {
	want, err := BuildConfig(sites, mode, cf)
	if err != nil {
		return false, err
	}
	cur, err := c.admin.Config(ctx)
	if err != nil {
		return false, err
	}
	return SameConfig(cur, want), nil
}
