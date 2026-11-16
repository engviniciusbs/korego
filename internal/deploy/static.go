package deploy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/store"
)

// ErrReleaseGone indica que a versão publicada do deploy de origem não existe
// mais no disco (rollback de app estática).
var ErrReleaseGone = errors.New("a versão publicada desse deploy não está mais disponível")

// Padrões das apps estáticas.
const (
	defaultStaticImage     = "node:22-alpine"
	defaultStaticOutputDir = "dist"
	defaultStaticBaseDir   = "."
	// keepReleases é quantas versões (as mais recentes) o prune mantém.
	keepReleases = 5
)

// StaticPublishRoot devolve a raiz de publicação da app: PublishRoot ou
// <WWWRoot>/<slug>.
func StaticPublishRoot(app store.App) string {
	if app.PublishRoot != "" {
		return app.PublishRoot
	}
	return filepath.Join(wwwRoot, app.Slug)
}

func staticBaseDir(app store.App) string {
	if app.StaticBaseDir == "" {
		return defaultStaticBaseDir
	}
	return app.StaticBaseDir
}

func staticOutputDir(app store.App) string {
	if app.StaticOutputDir == "" {
		return defaultStaticOutputDir
	}
	return app.StaticOutputDir
}

// StaticBuildCmd monta o docker run descartável do build: código em /app,
// -w /app/<baseDir>, limites de CPU/memória e o comando via sh -c. As
// variáveis vão como "-e NOME" (o valor segue só em Cmd.Env, nunca em Args).
// Devolve false quando não há build (sem comando e sem package.json).
func StaticBuildCmd(app store.App, srcDir string, env []string) (Cmd, bool) {
	base := staticBaseDir(app)
	cmd := app.StaticBuildCmd
	if cmd == "" {
		cmd = DetectBuildCommand(filepath.Join(srcDir, filepath.FromSlash(base)))
	}
	if cmd == "" {
		return Cmd{}, false
	}
	image := app.StaticImage
	if image == "" {
		image = defaultStaticImage
	}
	args := []string{"run", "--rm", "--cpus", "2", "--memory", "2g",
		"-v", srcDir + ":/app", "-w", path.Join("/app", base)}
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		args = append(args, "-e", name)
	}
	args = append(args, image, "sh", "-c", cmd)
	return Cmd{Name: "docker", Args: args, Env: env}, true
}

// buildContainerName é o nome do container de build de um deploy estático,
// para removê-lo se o deploy for cancelado ou interrompido.
func buildContainerName(deployID int64) string {
	return "korego-static-build-" + strconv.FormatInt(deployID, 10)
}

func (p *pipeline) static() bool { return p.app.BuildType == "static" }

// staticRoot valida e devolve a raiz de publicação resolvida (dentro de
// wwwRoot), criando-a se preciso. A adoção grava a raiz já resolvida: com
// wwwRoot atrás de um link (/var/www → /srv/www), ela é comparada também com
// o wwwRoot resolvido.
func (p *pipeline) staticRoot() (string, error) {
	root := filepath.Clean(StaticPublishRoot(p.app))
	if !filepath.IsAbs(root) || !underWWW(root) {
		return "", failWith(nil, "raiz de publicação inválida: %s", root)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fail("falha ao criar a raiz de publicação", err)
	}
	real, err := InsideRoot(wwwRoot, root)
	if err != nil {
		return "", fail("raiz de publicação inválida", err)
	}
	return real, nil
}

// underWWW diz se root é uma subpasta de wwwRoot, pelo caminho como está ou
// pelos dois resolvidos (o root pode ainda não existir).
func underWWW(root string) bool {
	www := filepath.Clean(wwwRoot)
	if root != www && within(www, root) {
		return true
	}
	realWWW, err := filepath.EvalSymlinks(www)
	if err != nil {
		return false
	}
	realRoot := root
	if r, err := filepath.EvalSymlinks(root); err == nil {
		realRoot = r
	}
	realWWW, realRoot = filepath.Clean(realWWW), filepath.Clean(realRoot)
	return realRoot != realWWW && within(realWWW, realRoot)
}

// runStatic é o pipeline da app estática: clone → build descartável →
// versão nova em releases/ → troca do current → deploy atual → prune. Antes
// da troca, erro ou cancelamento apagam a versão nova e o current fica como
// estava. Rollback só troca o current para a versão do deploy de origem.
func (p *pipeline) runStatic(ctx context.Context) error {
	s := p.s
	bg := context.WithoutCancel(ctx)
	root, err := p.staticRoot()
	if err != nil {
		return err
	}
	rollback := p.d.RollbackOf != nil
	var release string
	if rollback {
		release = p.d.ReleaseDir
		p.out.line(fmt.Sprintf("Rollback do deploy #%d para a versão %s", *p.d.RollbackOf, filepath.Base(release)))
		if !releaseExists(release) {
			return failWith(ErrReleaseGone, "a versão %s não existe mais", release)
		}
		if real, err := InsideRoot(root, release); err != nil || filepath.Dir(real) != filepath.Join(root, "releases") {
			return failWith(err, "versão inválida: %s", release)
		}
	} else {
		if release, err = p.buildStatic(ctx, root); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.setStatus(bg, p.d.ID, p.d.AppID, "starting", "")
	cur, err := s.DB.GetApp(ctx, p.app.ID)
	if err != nil {
		return fail("falha ao ler a aplicação", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.out.step("==> Trocando a versão publicada")
	if err := SwapCurrent(root, release); err != nil {
		return fail("falha ao trocar o current", err)
	}
	// O current já é a versão nova: daqui em diante nada é desfeito.
	p.newRelease = ""
	p.out.line("current → " + release)
	p.record(bg, "")
	var prev *store.Deploy
	if cur.CurrentDeployID != nil && *cur.CurrentDeployID != p.d.ID {
		if d, err := s.DB.GetDeploy(bg, *cur.CurrentDeployID); err == nil {
			prev = &d
		}
	}
	p.markRunning(bg, prev)
	if !rollback {
		p.out.step("==> Limpando")
		prevRelease := ""
		if prev != nil {
			prevRelease = prev.ReleaseDir
		}
		if err := PruneReleases(root, keepReleases, release, prevRelease); err != nil {
			p.out.line("limpeza de versões: " + err.Error())
		}
	}
	p.out.line("Deploy concluído")
	return nil
}

// buildStatic busca o código, roda o build (se houver) e publica a saída em
// uma versão nova, gravada no deploy. Devolve a pasta da versão.
func (p *pipeline) buildStatic(ctx context.Context, root string) (string, error) {
	s := p.s
	if p.app.SourceType != "github" && p.app.SourceType != "git" {
		return "", failWith(nil, "origem inválida para app estática: %s", p.app.SourceType)
	}
	base, outDir := staticBaseDir(p.app), staticOutputDir(p.app)
	if !ValidRelPath(base) {
		return "", failWith(nil, "diretório base inválido: %s", base)
	}
	if !ValidRelPath(outDir) {
		return "", failWith(nil, "pasta de saída inválida: %s", outDir)
	}
	if err := p.fetchSource(ctx); err != nil {
		return "", err
	}
	src, err := filepath.Abs(p.dir)
	if err != nil {
		return "", fail("falha ao resolver o diretório do clone", err)
	}
	if _, err := InsideRoot(src, filepath.FromSlash(base)); err != nil {
		return "", fail("diretório base inválido", err)
	}
	if cmd, ok := StaticBuildCmd(p.app, src, p.env); ok {
		p.out.step("==> Construindo")
		name := buildContainerName(p.d.ID)
		// --name entra antes da imagem (os 4 últimos args: imagem sh -c <cmd>).
		i := len(cmd.Args) - 4
		cmd.Args = append(cmd.Args[:i:i], append([]string{"--name", name}, cmd.Args[i:]...)...)
		if err := s.Runner.Run(ctx, cmd, p.out); err != nil {
			// Matar o cliente docker não para o container: remove pelo nome.
			if rerr := s.Docker.RemoveForce(context.WithoutCancel(ctx), name); rerr != nil && !errors.Is(rerr, docker.ErrNotFound) {
				log.Printf("deploy %d: remover container de build: %v", p.d.ID, rerr)
			}
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fail("falha no build", err)
		}
	} else {
		p.out.line("Sem package.json: a pasta de saída é publicada como está")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// O .git nunca é publicado (saída "." publicaria o histórico do repositório).
	if err := os.RemoveAll(filepath.Join(src, ".git")); err != nil {
		return "", fail("falha ao remover o .git antes de publicar", err)
	}
	out, err := InsideRoot(src, filepath.Join(src, filepath.FromSlash(base), filepath.FromSlash(outDir)))
	if err != nil {
		return "", fail("pasta de saída inválida", err)
	}
	p.out.step("==> Publicando")
	name := ReleaseName(s.now(), p.sha)
	release, err := PublishRelease(out, root, name, p.app.StaticSPA)
	if err != nil {
		return "", fail("falha ao publicar a versão", err)
	}
	p.newRelease = release
	p.out.line("Versão " + name + " publicada")
	if err := s.DB.SetDeployRelease(ctx, p.d.ID, release); err != nil {
		return "", fail("falha ao gravar a versão do deploy", err)
	}
	p.d.ReleaseDir = release
	return release, nil
}

func releaseExists(dir string) bool {
	if dir == "" {
		return false
	}
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

// sameRelease compara duas pastas de versão depois de resolver links.
func sameRelease(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if r, err := filepath.EvalSymlinks(a); err == nil {
		a = r
	}
	if r, err := filepath.EvalSymlinks(b); err == nil {
		b = r
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// recoverStatic trata um deploy estático interrompido: se o current já é a
// versão dele, ele vira o deploy atual (running); senão fica failed e a versão
// nova (nunca a de origem de um rollback) é apagada.
func (s *Service) recoverStatic(ctx context.Context, app store.App, d store.Deploy) {
	if err := s.Docker.RemoveForce(ctx, buildContainerName(d.ID)); err != nil && !errors.Is(err, docker.ErrNotFound) {
		log.Printf("deploy %d: remover container de build: %v", d.ID, err)
	}
	root := StaticPublishRoot(app)
	cur, cerr := CurrentRelease(root)
	if cerr == nil && sameRelease(cur, d.ReleaseDir) {
		if err := s.DB.SetCurrentDeploy(ctx, app.ID, d.ID); err != nil {
			log.Printf("deploy %d: definir deploy atual: %v", d.ID, err)
		}
		s.promoteInterrupted(ctx, d)
		return
	}
	s.publish(d.ID, d.AppID, d.Status, d.Reason)
	if d.RollbackOf == nil && releaseExists(d.ReleaseDir) && !sameRelease(cur, d.ReleaseDir) {
		if _, err := InsideRoot(filepath.Join(root, "releases"), d.ReleaseDir); err != nil {
			log.Printf("deploy %d: versão fora da raiz, mantida: %v", d.ID, err)
		} else if err := os.RemoveAll(d.ReleaseDir); err != nil {
			log.Printf("deploy %d: apagar versão %s: %v", d.ID, d.ReleaseDir, err)
		}
	}
}
