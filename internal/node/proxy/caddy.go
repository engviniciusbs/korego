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
	// Descends diz se pid descende de ancestor (padrão: cadeia de PPID em /proc).
	Descends func(pid, ancestor int) bool

	mu         sync.Mutex
	last       Status
	pendingKey string // estado em que a recriação pendente já foi logada
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
		Descends:    descendsOf,
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
			c.notePending("")
			return c.use(ctx, ct, running)
		}
		// Spec antiga: só recria após um "não serve" definitivo. Servindo, ou
		// sem resposta clara (starting, erro), a recriação fica pendente.
		if running {
			if st, v := c.probe(ctx, ct); v != notServing {
				c.notePending(string(st.State))
				return st
			}
		}
		c.notePending("")
		// /var/www precisa existir antes de derrubar o container antigo.
		if err := c.ensureWWW(); err != nil {
			return failure("criar "+wwwDir, err)
		}
		_ = c.docker.Stop(ctx, ct.ID) // pode já estar parado
		if err := c.docker.RemoveForce(ctx, ct.ID); err != nil {
			return failure("remover "+ContainerName, err)
		}
		return c.create(ctx, false)
	}
	return c.create(ctx, true)
}

func (c *Caddy) ensureWWW() error {
	if c.EnsureWWW == nil {
		return nil
	}
	return c.EnsureWWW()
}

// notePending loga "recriação pendente" uma vez por mudança de estado; key ""
// limpa a marca.
func (c *Caddy) notePending(key string) {
	c.mu.Lock()
	changed := c.pendingKey != key
	c.pendingKey = key
	c.mu.Unlock()
	if changed && key != "" {
		log.Printf("proxy: recriação pendente (spec do %s desatualizada; estado %s)", ContainerName, key)
	}
}

// use usa o container existente, iniciando-o se estiver parado.
func (c *Caddy) use(ctx context.Context, ct docker.Container, running bool) Status {
	if !running {
		if err := c.docker.Start(ctx, ct.ID); err != nil {
			return failure("iniciar "+ContainerName, err)
		}
	}
	st, _ := c.probe(ctx, ct)
	return st
}

func (c *Caddy) create(ctx context.Context, ensureWWW bool) Status {
	if err := c.ensureImage(ctx); err != nil {
		return failure("baixar "+CaddyImage, err)
	}
	if ensureWWW {
		if err := c.ensureWWW(); err != nil {
			return failure("criar "+wwwDir, err)
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
	if list, lerr := c.docker.List(ctx); lerr == nil {
		for _, ct := range list {
			if ct.Name == ContainerName && ct.Managed {
				st, _ := c.probe(ctx, ct)
				return st
			}
		}
	}
	st, _ := c.probe(ctx, docker.Container{})
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

// verdict é a resposta de probe sobre o Caddy estar servindo 80/443.
type verdict int

const (
	unknown    verdict = iota // sem resposta clara (starting, erro)
	serving                   // config escuta :443 e as portas são do nosso container
	notServing                // a Admin API respondeu e o Caddy não serve 80/443
)

// adminPort é a porta da Admin API do Caddy (loopback).
const adminPort = 2019

// probe consulta a Admin API e as portas. Antes de falar com a Admin API,
// confere que quem escuta em 127.0.0.1:2019 é o nosso container: um Caddy do
// host ocupando a 2019 nunca recebe a nossa config (estado error).
//
// "Nosso" é decidido pelo PID: o dono da porta é o PID do container (docker
// inspect) ou descende dele. Nomes de processo não contam.
//
// Nas portas 80/443, o dono é alheio quando não é nosso, ou quando a config
// atual não escuta em :443 (config vazia ou de ensaio: o Caddy não as segura).
func (c *Caddy) probe(ctx context.Context, ct docker.Container) (Status, verdict) {
	pid, err := c.containerPid(ctx, ct)
	if err != nil {
		// Sem o PID não dá para saber de quem são as portas: não conclui nada.
		return Status{State: StateStarting, Message: "falha ao inspecionar o container " + ContainerName}, unknown
	}
	if owner, busy := c.PortOwner(adminPort); busy && !c.ours(owner, pid) {
		return Status{
			State:   StateError,
			Process: owner,
			Message: fmt.Sprintf("a porta %d da Admin API está em uso por %s", adminPort, owner),
		}, unknown
	}
	pctx, cancel := context.WithTimeout(ctx, adminPingTimeout)
	defer cancel()
	cfg, err := c.admin.Config(pctx)
	if err != nil {
		return Status{State: StateStarting, Message: "aguardando a Admin API do Caddy"}, unknown
	}
	listens := listensOn443(cfg)
	for _, port := range []int{80, 443} {
		owner, busy := c.PortOwner(port)
		if busy && (!listens || !c.ours(owner, pid)) {
			return Status{
				State:   StateTrial,
				Mode:    ListenTrial,
				Process: owner,
				Message: "Proxy em modo ensaio: as portas 80/443 estão em uso por " + owner,
			}, notServing
		}
	}
	if listens {
		return Status{State: StateActive, Mode: ListenNormal}, serving
	}
	return Status{State: StateActive, Mode: ListenNormal}, notServing
}

// containerPid devolve o PID do container no host (0 se ainda não há container).
func (c *Caddy) containerPid(ctx context.Context, ct docker.Container) (int, error) {
	if ct.ID == "" {
		return 0, nil
	}
	d, err := c.docker.Inspect(ctx, ct.ID)
	if err != nil {
		return 0, err
	}
	return d.Pid, nil
}

// ours diz se o dono ("nome (pid N)") é o processo do container ou descende dele.
func (c *Caddy) ours(owner string, containerPid int) bool {
	p := ownerPid(owner)
	if p <= 0 || containerPid <= 0 {
		return false
	}
	return p == containerPid || (c.Descends != nil && c.Descends(p, containerPid))
}

// ownerPid extrai N de "nome (pid N)"; 0 se não houver.
func ownerPid(owner string) int {
	i := strings.LastIndex(owner, "(pid ")
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSuffix(owner[i+len("(pid "):], ")"))
	if err != nil {
		return 0
	}
	return n
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
	// Simétrico: normal em ensaio derrubaria o Caddy ao disputar 80/443 e
	// ensaio num Caddy servindo tiraria :80/:443 do ar.
	if (state == StateTrial && mode == ListenNormal) || (state == StateActive && mode == ListenTrial) {
		return ErrTrialMode
	}
	cfg, err := BuildConfig(sites, mode, cf)
	if err != nil {
		return err
	}
	return c.admin.Load(ctx, cfg)
}

// Switch carrega a config no modo pedido sem os guardas de Apply e, com a
// carga aceita, passa o último estado para o do modo: normal → active/normal,
// trial → trial. Só o motor de migração (internal/migrate) usa, com a
// reconciliação e os deploys parados. Carga recusada (porta ocupada, por
// exemplo) mantém a config e o estado anteriores: o Caddy recusa a carga
// inteira. ErrInactive se o último Ensure não terminou em active ou trial.
func (c *Caddy) Switch(ctx context.Context, sites []Site, mode Listen, cf []string) error {
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
	if err := c.admin.Load(ctx, cfg); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if mode == ListenTrial {
		c.last = Status{State: StateTrial, Mode: ListenTrial, Message: "Proxy em modo ensaio"}
	} else {
		c.last = Status{State: StateActive, Mode: ListenNormal}
	}
	return nil
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
