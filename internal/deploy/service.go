package deploy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/jobs"
	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/secrets"
	"github.com/engviniciusbs/korego/internal/store"
)

// Erros do serviço de deploy (use errors.Is).
var (
	ErrNoGitHub            = errors.New("o GitHub não está conectado")
	ErrRollbackUnsupported = errors.New("rollback não é suportado para aplicações Compose")
	ErrImageGone           = errors.New("a imagem desse deploy não está mais disponível")
	ErrDeployFinished      = errors.New("o deploy já terminou")
)

// StatusTopic é o tópico do hub com as mudanças de status dos deploys.
const StatusTopic = "deploys"

// defaultHealthTimeout vale quando a aplicação não define health_timeout_s.
const defaultHealthTimeout = 60 * time.Second

// overrideName é o arquivo override do Compose gravado no diretório do clone.
const overrideName = "docker-compose.korego.yml"

// Router reaplica as rotas do proxy de uma aplicação (domains.Service).
type Router interface {
	ReapplyApp(ctx context.Context, appID int64) error
}

// TokenSource fornece o token de instalação do GitHub para clonar repo.
// nil no Service = GitHub não conectado.
type TokenSource interface {
	CloneToken(ctx context.Context, repo string) (string, error)
}

// StatusEvent é publicado em StatusTopic a cada mudança de status.
type StatusEvent struct {
	ID     int64  `json:"id"`
	AppID  int64  `json:"appId"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// Service orquestra os deploys: fila → código → build → container → saúde →
// troca da rota → limpeza. Um deploy que falha nunca derruba a versão no ar.
type Service struct {
	DB     *store.DB
	Docker docker.Client
	Runner Runner
	Box    *secrets.Box
	Hub    *events.Hub
	Queue  *jobs.Queue
	Router Router
	Tokens TokenSource
	// DataDir guarda builds/ e logs/.
	DataDir string
	Now     func() time.Time
	// Dial testa a porta quando o container não tem healthcheck (padrão:
	// net.DialTimeout de 2 s).
	Dial func(addr string) error
	// AllowFileURLs aceita file:// em origens Git; só testes de integração.
	AllowFileURLs bool

	// listen testa se uma porta está livre (padrão TryListen).
	listen func(addr string) error
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) dial() func(string) error {
	if s.Dial != nil {
		return s.Dial
	}
	return func(addr string) error {
		c, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			return err
		}
		return c.Close()
	}
}

func (s *Service) listenFn() func(string) error {
	if s.listen != nil {
		return s.listen
	}
	return TryListen
}

func (s *Service) buildDir(id int64) string {
	return filepath.Join(s.DataDir, "builds", strconv.FormatInt(id, 10))
}

// removeBuildDir apaga builds/<id> e o plano do railpack ao lado dele.
func (s *Service) removeBuildDir(id int64) {
	dir := s.buildDir(id)
	if err := os.RemoveAll(dir); err != nil {
		log.Printf("deploy %d: remover %s: %v", id, dir, err)
	}
	_ = os.Remove(filepath.Clean(dir) + ".railpack-plan.json")
}

func (s *Service) publish(id, appID int64, status, reason string) {
	if s.Hub != nil {
		s.Hub.Publish(StatusTopic, StatusEvent{ID: id, AppID: appID, Status: status, Reason: reason})
	}
}

// setStatus grava e publica o status. Erros só vão para o log do servidor.
func (s *Service) setStatus(ctx context.Context, id, appID int64, status, reason string) {
	if err := s.DB.UpdateDeployStatus(ctx, id, status, reason, s.now()); err != nil {
		log.Printf("deploy %d: gravar status %s: %v", id, status, err)
		return
	}
	s.publish(id, appID, status, reason)
}

// Deploy grava um deploy queued e o coloca na fila da aplicação. Um pedido que
// esperava na mesma aplicação vira superseded. sha e msg (opcionais) vêm do
// webhook e são trocados pelos do clone.
func (s *Service) Deploy(ctx context.Context, appID int64, trigger, sha, msg string) (store.Deploy, error) {
	if trigger != "manual" && trigger != "webhook" {
		return store.Deploy{}, fmt.Errorf("gatilho inválido: %q", trigger)
	}
	app, err := s.DB.GetApp(ctx, appID)
	if err != nil {
		return store.Deploy{}, err
	}
	if app.SourceType == "github" && s.Tokens == nil {
		return store.Deploy{}, ErrNoGitHub
	}
	d := store.Deploy{AppID: appID, Trigger: trigger, Status: "queued", CommitSHA: sha, CommitMessage: msg}
	if err := s.insertAndEnqueue(ctx, &d); err != nil {
		return store.Deploy{}, err
	}
	return d, nil
}

// Rollback cria um deploy que sobe de novo a imagem de fromDeployID, sem
// buscar código nem construir.
func (s *Service) Rollback(ctx context.Context, appID, fromDeployID int64) (store.Deploy, error) {
	app, err := s.DB.GetApp(ctx, appID)
	if err != nil {
		return store.Deploy{}, err
	}
	if app.BuildType == "compose" {
		return store.Deploy{}, ErrRollbackUnsupported
	}
	src, err := s.DB.GetDeploy(ctx, fromDeployID)
	if err != nil {
		return store.Deploy{}, err
	}
	if src.AppID != appID {
		return store.Deploy{}, fmt.Errorf("deploy %d: %w", fromDeployID, store.ErrNotFound)
	}
	keep, err := s.DB.LatestImages(ctx, appID, 3)
	if err != nil {
		return store.Deploy{}, err
	}
	if src.Image == "" || !slices.Contains(keep, src.Image) {
		return store.Deploy{}, ErrImageGone
	}
	from := fromDeployID
	d := store.Deploy{AppID: appID, Trigger: "rollback", Status: "queued", Image: src.Image,
		CommitSHA: src.CommitSHA, CommitMessage: src.CommitMessage, RollbackOf: &from}
	if err := s.insertAndEnqueue(ctx, &d); err != nil {
		return store.Deploy{}, err
	}
	return d, nil
}

func (s *Service) insertAndEnqueue(ctx context.Context, d *store.Deploy) error {
	if err := s.DB.InsertDeploy(ctx, d); err != nil {
		return err
	}
	s.publish(d.ID, d.AppID, d.Status, d.Reason)
	s.enqueue(*d)
	return nil
}

func (s *Service) enqueue(d store.Deploy) {
	id, appID := d.ID, d.AppID
	s.Queue.Enqueue(jobs.Job{
		Key: "app:" + strconv.FormatInt(appID, 10),
		ID:  id,
		Run: func(ctx context.Context) { s.run(ctx, id) },
		OnSuperseded: func() {
			s.setStatus(context.Background(), id, appID, "superseded", "substituído por um pedido mais novo")
		},
	})
}

// Cancel tira da fila um deploy em espera (vira cancelled na hora) ou cancela
// o que está rodando (o pipeline limpa e grava cancelled).
func (s *Service) Cancel(ctx context.Context, deployID int64) error {
	d, err := s.DB.GetDeploy(ctx, deployID)
	if err != nil {
		return err
	}
	if !s.Queue.Cancel(deployID) {
		return ErrDeployFinished
	}
	// Em espera, a fila não chama ninguém: o status é gravado aqui. Se o job
	// tinha acabado de começar, o pipeline vê o ctx cancelado e termina em
	// cancelled do mesmo jeito.
	if d.Status == "queued" {
		s.setStatus(ctx, d.ID, d.AppID, "cancelled", "cancelado")
	}
	return nil
}

// Recover roda na subida: deploys building/starting viram failed
// ("interrompido"), os containers que eles criaram são removidos (menos o do
// deploy atual da aplicação) e os queued voltam para a fila, em ordem.
func (s *Service) Recover(ctx context.Context) error {
	interrupted, err := s.DB.MarkInterrupted(ctx, s.now())
	if err != nil {
		return err
	}
	for _, d := range interrupted {
		s.publish(d.ID, d.AppID, d.Status, d.Reason)
		app, err := s.DB.GetApp(ctx, d.AppID)
		current := err == nil && app.CurrentDeployID != nil && *app.CurrentDeployID == d.ID
		if !current {
			s.removeDeployContainers(ctx, d.ID)
			if err == nil {
				s.restorePort(ctx, app, d)
			}
		}
		s.removeBuildDir(d.ID)
	}
	queued, err := s.DB.QueuedDeploys(ctx)
	if err != nil {
		return err
	}
	for _, d := range queued {
		s.enqueue(d)
	}
	return nil
}

func (s *Service) removeDeployContainers(ctx context.Context, id int64) {
	cs, err := s.Docker.ListByLabel(ctx, "korego.deploy", strconv.FormatInt(id, 10))
	if err != nil {
		log.Printf("deploy %d: listar containers: %v", id, err)
		return
	}
	for _, c := range cs {
		if err := s.Docker.RemoveForce(ctx, c.ID); err != nil {
			log.Printf("deploy %d: remover container %s: %v", id, c.ID, err)
		}
	}
}

// restorePort desfaz uma troca interrompida no meio: se a app ficou apontando
// para a porta do deploy interrompido, volta para a do deploy atual. As rotas
// são reaplicadas na subida pelo serviço de domínios.
func (s *Service) restorePort(ctx context.Context, app store.App, d store.Deploy) {
	if app.BuildType == "compose" || d.HostPort == 0 || app.HostPort != d.HostPort {
		return
	}
	port := 0
	if app.CurrentDeployID != nil {
		if cur, err := s.DB.GetDeploy(ctx, *app.CurrentDeployID); err == nil {
			port = cur.HostPort
		}
	}
	app.HostPort = port
	if err := s.DB.UpdateApp(ctx, &app); err != nil {
		log.Printf("deploy %d: restaurar porta da app: %v", d.ID, err)
	}
}

// failure carrega o motivo, em português, gravado no deploy.
type failure struct {
	reason string
	err    error
}

func (f *failure) Error() string { return f.reason }
func (f *failure) Unwrap() error { return f.err }

func failWith(err error, format string, a ...any) error {
	return &failure{reason: fmt.Sprintf(format, a...), err: err}
}

func fail(prefix string, err error) error {
	return &failure{reason: prefix + ": " + err.Error(), err: err}
}

// pipeline é a execução de um deploy.
type pipeline struct {
	s       *Service
	d       store.Deploy
	app     store.App
	out     *logChain
	env     []string
	secrets []string
	token   string
	image   string
	sha     string
	msg     string
	port    int
	cid     string // container novo (não-Compose) a remover se algo falhar
}

func (s *Service) run(ctx context.Context, id int64) {
	bg := context.WithoutCancel(ctx)
	d, err := s.DB.GetDeploy(bg, id)
	if err != nil {
		log.Printf("deploy %d: %v", id, err)
		return
	}
	if d.Status != "queued" {
		return // cancelado ou substituído antes de começar
	}
	if ctx.Err() != nil {
		s.setStatus(bg, d.ID, d.AppID, "cancelled", "cancelado")
		return
	}
	app, err := s.DB.GetApp(bg, d.AppID)
	if err != nil {
		s.setStatus(bg, d.ID, d.AppID, "failed", "aplicação não encontrada")
		return
	}
	defer s.removeBuildDir(id)
	p := &pipeline{s: s, d: d, app: app, sha: d.CommitSHA, msg: d.CommitMessage}
	p.execute(ctx)
}

func (p *pipeline) execute(ctx context.Context) {
	bg := context.WithoutCancel(ctx)
	s := p.s
	logf, err := OpenDeployLog(s.DataDir, p.d.ID, s.Hub)
	if err != nil {
		s.setStatus(bg, p.d.ID, p.d.AppID, "failed", "não foi possível abrir o log do deploy")
		return
	}
	s.setStatus(bg, p.d.ID, p.d.AppID, "building", "")
	prepErr := p.prepare(ctx)
	p.out = newLogChain(logf, p.secrets)
	defer p.out.Close()
	err = prepErr
	if err == nil {
		err = p.steps(ctx)
	}
	if err != nil {
		p.fail(ctx, err)
	}
}

// prepare decifra as variáveis e obtém o token do GitHub; tudo vira segredo
// do Redactor.
func (p *pipeline) prepare(ctx context.Context) error {
	enc, err := p.s.DB.AppEnvEncrypted(ctx, p.app.ID)
	if err != nil {
		return fail("falha ao ler as variáveis", err)
	}
	names := make([]string, 0, len(enc))
	for n := range enc {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		v, err := p.s.Box.Decrypt(enc[n])
		if err != nil {
			return failWith(err, "não foi possível decifrar a variável %s", n)
		}
		p.env = append(p.env, n+"="+string(v))
		p.secrets = append(p.secrets, string(v))
	}
	if p.app.SourceType == "github" && p.d.RollbackOf == nil {
		if p.s.Tokens == nil {
			return failWith(ErrNoGitHub, "o GitHub não está conectado")
		}
		tok, err := p.s.Tokens.CloneToken(ctx, p.app.Repo)
		if err != nil {
			return fail("falha ao obter o token do GitHub", err)
		}
		p.token = tok
		p.secrets = append(p.secrets, TokenSecrets(tok)...)
	}
	return nil
}

func (p *pipeline) compose() bool { return p.app.BuildType == "compose" }

func (p *pipeline) steps(ctx context.Context) error {
	if p.d.RollbackOf != nil {
		p.image = p.d.Image
		p.out.line(fmt.Sprintf("Rollback do deploy #%d com a imagem %s", *p.d.RollbackOf, p.image))
	} else if err := p.source(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.s.setStatus(context.WithoutCancel(ctx), p.d.ID, p.d.AppID, "starting", "")
	if p.compose() {
		cid, err := p.startCompose(ctx)
		if err != nil {
			return err
		}
		if err := p.health(ctx, cid); err != nil {
			return err
		}
		if err := p.swapCompose(ctx, cid); err != nil {
			return err
		}
	} else {
		if err := p.startContainer(ctx); err != nil {
			return err
		}
		if err := p.health(ctx, p.cid); err != nil {
			return err
		}
		if err := p.swap(ctx); err != nil {
			return err
		}
	}
	p.cleanup(context.WithoutCancel(ctx))
	return nil
}

// source busca o código e constrói (git/github), ou puxa e etiqueta (image).
func (p *pipeline) source(ctx context.Context) error {
	s, app := p.s, p.app
	switch app.SourceType {
	case "github", "git":
		p.out.step("==> Buscando código")
		url := app.Repo
		if app.SourceType == "github" {
			if !ValidRepo(app.Repo) {
				return failWith(nil, "repositório inválido: %s", app.Repo)
			}
			url = "https://github.com/" + app.Repo + ".git"
		}
		if err := os.MkdirAll(filepath.Join(s.DataDir, "builds"), 0o700); err != nil {
			return fail("falha ao criar o diretório de build", err)
		}
		dir := s.buildDir(p.d.ID)
		sha, msg, err := fetch(ctx, s.Runner, dir, Source{URL: url, Branch: app.Branch, Token: p.token}, p.out, s.AllowFileURLs)
		if err != nil {
			return fail("falha ao buscar o código", err)
		}
		p.sha, p.msg = sha, msg
		p.out.step("==> Construindo")
		if !p.compose() {
			p.image = ImageForApp(app, sha, "")
		}
		if err := Build(ctx, s.Runner, app, dir, p.image, p.out); err != nil {
			if errors.Is(err, ErrRailpackMissing) {
				return failWith(err, "railpack não está instalado")
			}
			return fail("falha no build", err)
		}
	case "image":
		p.out.step("==> Baixando a imagem")
		id, err := PullImage(ctx, s.Docker, s.Runner, app.Image, p.out)
		if err != nil {
			return fail("falha ao baixar a imagem", err)
		}
		p.image = ImageForApp(app, "", id)
		if err := TagImage(ctx, s.Runner, app.Image, p.image, p.out); err != nil {
			return fail("falha ao etiquetar a imagem", err)
		}
	default:
		return failWith(nil, "origem desconhecida: %s", app.SourceType)
	}
	return nil
}

// retryableStart diz se vale tentar de novo com outra porta: porta ocupada
// (corrida com outro processo) ou nome em conflito (container velho).
func retryableStart(err error) bool {
	if errors.Is(err, docker.ErrConflict) {
		return true
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "port is already allocated") || strings.Contains(m, "address already in use") ||
		strings.Contains(m, "bind for")
}

func (p *pipeline) startContainer(ctx context.Context) error {
	s := p.s
	p.out.step("==> Subindo container")
	used, err := s.DB.UsedHostPorts(ctx)
	if err != nil {
		return fail("falha ao escolher a porta", err)
	}
	port, err := FreePort(used, s.listenFn())
	if err != nil {
		return fail("falha ao escolher a porta", err)
	}
	cid, err := StartContainer(ctx, s.Docker, p.app, p.d.ID, p.image, p.env, port)
	if err != nil && ctx.Err() == nil && retryableStart(err) {
		if errors.Is(err, docker.ErrConflict) {
			name := containerName(p.app.Slug, p.d.ID, p.image)
			p.out.line("Removendo container antigo com o mesmo nome: " + name)
			if rerr := s.Docker.RemoveForce(ctx, name); rerr != nil {
				p.out.line("não foi possível remover " + name + ": " + rerr.Error())
			}
		} else {
			p.out.line(fmt.Sprintf("Porta %d ocupada, tentando outra", port))
		}
		used[port] = true
		if port, err = FreePort(used, s.listenFn()); err != nil {
			return fail("falha ao escolher a porta", err)
		}
		cid, err = StartContainer(ctx, s.Docker, p.app, p.d.ID, p.image, p.env, port)
	}
	if err != nil {
		if errors.Is(err, docker.ErrNotFound) {
			return failWith(err, "imagem não encontrada: %s", p.image)
		}
		return fail("falha ao subir o container", err)
	}
	p.cid, p.port = cid, port
	p.out.line(fmt.Sprintf("Container %s em 127.0.0.1:%d", shortID(cid), port))
	// Grava já a porta: reserva a vaga e deixa o container rastreável.
	if err := s.DB.SetDeployResult(ctx, p.d.ID, p.image, port, cid, p.sha, p.msg); err != nil {
		return fail("falha ao gravar o deploy", err)
	}
	return nil
}

func (p *pipeline) startCompose(ctx context.Context) (string, error) {
	s := p.s
	p.out.step("==> Subindo container")
	port := p.app.HostPort
	if port == 0 {
		used, err := s.DB.UsedHostPorts(ctx)
		if err != nil {
			return "", fail("falha ao escolher a porta", err)
		}
		if port, err = FreePort(used, s.listenFn()); err != nil {
			return "", fail("falha ao escolher a porta", err)
		}
		a, err := s.DB.GetApp(ctx, p.app.ID)
		if err != nil {
			return "", fail("falha ao gravar a porta", err)
		}
		a.HostPort = port
		if err := s.DB.UpdateApp(ctx, &a); err != nil {
			return "", fail("falha ao gravar a porta", err)
		}
		p.app.HostPort = port
	}
	p.port = port
	data, err := ComposeOverride(p.app, port)
	if err != nil {
		return "", fail("falha ao gerar o override do Compose", err)
	}
	dir := s.buildDir(p.d.ID)
	override := filepath.Join(dir, overrideName)
	if err := os.WriteFile(override, data, 0o600); err != nil {
		return "", fail("falha ao gravar o override do Compose", err)
	}
	if err := ComposeUp(ctx, s.Runner, p.app, dir, override, p.out); err != nil {
		return "", fail("falha ao subir o Compose", err)
	}
	cs, err := s.Docker.ListByLabel(ctx, "com.docker.compose.project", "korego-"+p.app.Slug)
	if err != nil {
		return "", fail("falha ao listar os containers do Compose", err)
	}
	for _, c := range cs {
		if c.Service == p.app.ComposeService {
			return c.ID, nil
		}
	}
	return "", failWith(nil, "container do serviço %s não encontrado", p.app.ComposeService)
}

func (p *pipeline) health(ctx context.Context, cid string) error {
	s := p.s
	p.out.step("==> Verificando saúde")
	timeout := time.Duration(p.app.HealthTimeoutS) * time.Second
	if timeout <= 0 {
		timeout = defaultHealthTimeout
	}
	err := WaitHealthy(ctx, s.Docker, cid, p.port, timeout, s.dial())
	switch {
	case err == nil:
		p.out.line("Container saudável")
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, ErrHealthTimeout):
		return failWith(err, "verificação de saúde falhou: tempo esgotado (%ds)", int(timeout/time.Second))
	case errors.Is(err, ErrContainerExited):
		if det, ierr := s.Docker.Inspect(context.WithoutCancel(ctx), cid); ierr == nil {
			return failWith(err, "o container saiu com código %d", det.ExitCode)
		}
		return failWith(err, "o container saiu")
	case errors.Is(err, ErrUnhealthy):
		return failWith(err, "verificação de saúde falhou: o container ficou unhealthy")
	default:
		return fail("verificação de saúde falhou", err)
	}
}

func (p *pipeline) reapply(ctx context.Context) error {
	if p.s.Router == nil {
		return nil
	}
	return p.s.Router.ReapplyApp(ctx, p.app.ID)
}

// swap troca a rota para o container novo, que já passou na verificação de
// saúde. Só depois de o proxy aceitar é que o container anterior sai.
func (p *pipeline) swap(ctx context.Context) error {
	s := p.s
	bg := context.WithoutCancel(ctx)
	p.out.step("==> Trocando a rota")
	cur, err := s.DB.GetApp(ctx, p.app.ID)
	if err != nil {
		return fail("falha ao ler a aplicação", err)
	}
	oldPort, prevID := cur.HostPort, cur.CurrentDeployID
	cur.HostPort = p.port
	if err := s.DB.UpdateApp(ctx, &cur); err != nil {
		return fail("falha ao gravar a porta", err)
	}
	if err := p.reapply(ctx); err != nil {
		// Volta a porta e a rota; o container anterior segue no ar.
		if back, gerr := s.DB.GetApp(bg, p.app.ID); gerr == nil {
			back.HostPort = oldPort
			if uerr := s.DB.UpdateApp(bg, &back); uerr != nil {
				log.Printf("deploy %d: restaurar porta: %v", p.d.ID, uerr)
			}
		}
		if rerr := p.reapply(bg); rerr != nil {
			log.Printf("deploy %d: reaplicar rota antiga: %v", p.d.ID, rerr)
		}
		return fail("falha ao atualizar o proxy", err)
	}
	// A rota já é do novo: daqui em diante nada é desfeito nem cancelado.
	newCID := p.cid
	p.cid = ""
	var prev *store.Deploy
	if prevID != nil && *prevID != p.d.ID {
		if d, err := s.DB.GetDeploy(bg, *prevID); err == nil {
			prev = &d
			if d.ContainerID != "" && d.ContainerID != newCID {
				if err := s.Docker.RemoveForce(bg, d.ContainerID); err != nil {
					p.out.line("não foi possível remover o container anterior: " + err.Error())
				} else {
					p.out.line("Container anterior removido: " + shortID(d.ContainerID))
				}
			}
		}
	}
	return p.finish(bg, newCID, prev)
}

// swapCompose: os containers já foram recriados pelo Compose na porta fixa;
// só falta garantir a rota (necessária no primeiro deploy).
func (p *pipeline) swapCompose(ctx context.Context, cid string) error {
	s := p.s
	bg := context.WithoutCancel(ctx)
	p.out.step("==> Trocando a rota")
	if err := p.reapply(ctx); err != nil {
		return fail("falha ao atualizar o proxy", err)
	}
	cur, err := s.DB.GetApp(bg, p.app.ID)
	if err != nil {
		return fail("falha ao ler a aplicação", err)
	}
	var prev *store.Deploy
	if cur.CurrentDeployID != nil && *cur.CurrentDeployID != p.d.ID {
		if d, err := s.DB.GetDeploy(bg, *cur.CurrentDeployID); err == nil {
			prev = &d
		}
	}
	return p.finish(bg, cid, prev)
}

// finish grava o resultado, aponta a app para o deploy e marca o anterior
// como superseded (D4).
func (p *pipeline) finish(ctx context.Context, cid string, prev *store.Deploy) error {
	s := p.s
	if err := s.DB.SetDeployResult(ctx, p.d.ID, p.image, p.port, cid, p.sha, p.msg); err != nil {
		log.Printf("deploy %d: gravar resultado: %v", p.d.ID, err)
	}
	if err := s.DB.SetCurrentDeploy(ctx, p.app.ID, p.d.ID); err != nil {
		log.Printf("deploy %d: definir deploy atual: %v", p.d.ID, err)
	}
	s.setStatus(ctx, p.d.ID, p.d.AppID, "running", "")
	if prev != nil && prev.Status == "running" {
		s.setStatus(ctx, prev.ID, prev.AppID, "superseded", "")
	}
	return nil
}

// cleanup remove imagens antigas (fora das 3 mais recentes). Falhas aqui não
// mudam o status: o deploy já está no ar.
func (p *pipeline) cleanup(ctx context.Context) {
	p.out.step("==> Limpando")
	if !p.compose() {
		keep, err := p.s.DB.LatestImages(ctx, p.app.ID, 3)
		if err == nil {
			err = Cleanup(ctx, p.s.Docker, p.app, keep, p.out)
		}
		if err != nil {
			p.out.line("limpeza de imagens: " + err.Error())
		}
	}
	p.out.line("Deploy concluído")
}

// fail desfaz o que o deploy criou (só o container novo), registra o motivo e
// grava failed, ou cancelled se ctx foi cancelado.
func (p *pipeline) fail(ctx context.Context, err error) {
	s := p.s
	bg := context.WithoutCancel(ctx)
	if p.cid != "" {
		if rerr := s.Docker.RemoveForce(bg, p.cid); rerr != nil {
			log.Printf("deploy %d: remover container novo: %v", p.d.ID, rerr)
		}
		p.cid = ""
	}
	status, reason := "failed", "erro: "+err.Error()
	var f *failure
	if errors.As(err, &f) {
		reason = f.reason
	}
	if ctx.Err() != nil {
		status, reason = "cancelled", "cancelado"
	}
	reason = redactString(reason, p.secrets)
	if status == "cancelled" {
		p.out.step("==> Cancelado")
	} else {
		p.out.step("==> Falhou: " + reason)
	}
	p.out.Close()
	s.setStatus(bg, p.d.ID, p.d.AppID, status, reason)
}

func redactString(s string, secrets []string) string {
	for _, sec := range secrets {
		if len(sec) >= minSecret {
			s = strings.ReplaceAll(s, sec, mask)
		}
	}
	return tokenRe.ReplaceAllString(s, mask)
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// logChain liga a saída do pipeline ao log: Redactor → lineSplitter → DeployLog.
// Nos limites de passo, fecha a linha em aberto e esvazia o Redactor.
type logChain struct {
	mu     sync.Mutex
	red    *Redactor
	split  *lineSplitter
	log    *DeployLog
	nl     bool // nada escrito ou o último byte foi '\n'
	closed bool
}

func newLogChain(l *DeployLog, secrets []string) *logChain {
	split := &lineSplitter{dst: l}
	return &logChain{red: NewRedactor(split, secrets), split: split, log: l, nl: true}
}

func (c *logChain) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(p) > 0 {
		c.nl = p[len(p)-1] == '\n'
	}
	return c.red.Write(p)
}

// line escreve uma linha própria (fechando a anterior, se aberta).
func (c *logChain) line(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.nl {
		_, _ = c.red.Write([]byte("\n"))
	}
	_, _ = c.red.Write([]byte(text + "\n"))
	c.nl = true
	_ = c.red.Flush()
}

// step marca o início de um passo.
func (c *logChain) step(header string) { c.line(header) }

// Close descarrega tudo e fecha o arquivo. Pode ser chamado mais de uma vez.
func (c *logChain) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	_ = c.red.Close()
	c.split.Flush()
	_ = c.log.Close()
}
