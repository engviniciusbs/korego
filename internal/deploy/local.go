package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/store"
)

// Erros das stacks compose gerenciadas no lugar (use errors.Is).
var (
	ErrStackNotFound = errors.New("nenhum container encontrado para o projeto compose")
	ErrStackLabels   = errors.New("os containers do projeto compose não têm os rótulos de pasta e arquivos do Compose")
)

// Rótulos que o Compose grava nos containers de um projeto.
const (
	labelComposeProject     = "com.docker.compose.project"
	labelComposeWorkingDir  = "com.docker.compose.project.working_dir"
	labelComposeConfigFiles = "com.docker.compose.project.config_files"
)

// maxOutputTail é quanto da saída de um comando entra na mensagem de erro.
const maxOutputTail = 2000

// Stack é uma stack compose existente, gerenciada no lugar. Pasta e arquivos
// vêm dos rótulos do container mais recente do projeto, lidos a cada uso.
type Stack struct {
	Project    string
	Dir        string
	Files      []string
	Containers []docker.Container
}

// FindStack acha a stack pelo projeto compose. Containers do KoreGO
// (korego.managed) são ignorados. Devolve ErrStackNotFound quando não há
// containers e ErrStackLabels quando faltam os rótulos de pasta ou arquivos.
func FindStack(ctx context.Context, d docker.Client, project string) (Stack, error) {
	if project == "" {
		return Stack{}, fmt.Errorf("%w: projeto vazio", ErrStackNotFound)
	}
	all, err := d.ListByLabel(ctx, labelComposeProject, project)
	if err != nil {
		return Stack{}, err
	}
	var cs []docker.Container
	for _, c := range all {
		if !c.Managed {
			cs = append(cs, c)
		}
	}
	if len(cs) == 0 {
		return Stack{}, fmt.Errorf("%w: %s", ErrStackNotFound, project)
	}
	// Mais novo primeiro; empate pelo nome.
	sort.SliceStable(cs, func(i, j int) bool {
		if !cs[i].CreatedAt.Equal(cs[j].CreatedAt) {
			return cs[i].CreatedAt.After(cs[j].CreatedAt)
		}
		return cs[i].Name < cs[j].Name
	})
	labels := cs[0].Labels
	dir := strings.TrimSpace(labels[labelComposeWorkingDir])
	var files []string
	for _, f := range strings.Split(labels[labelComposeConfigFiles], ",") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	if dir == "" || len(files) == 0 {
		return Stack{}, fmt.Errorf("%w: %s", ErrStackLabels, project)
	}
	if !filepath.IsAbs(dir) {
		return Stack{}, fmt.Errorf("%w: pasta não absoluta (%s)", ErrStackLabels, dir)
	}
	return Stack{Project: project, Dir: filepath.Clean(dir), Files: files, Containers: cs}, nil
}

// StackEnvPath é o .env da stack: <Dir>/.env.
func StackEnvPath(st Stack) string {
	return filepath.Join(st.Dir, ".env")
}

// stackCmd monta "docker compose -p <projeto> -f <arq>... <op...>" na pasta
// da stack. Nenhuma variável da app vai junto: a stack usa o próprio .env.
func stackCmd(st Stack, op ...string) Cmd {
	args := []string{"compose", "-p", st.Project}
	for _, f := range st.Files {
		args = append(args, "-f", f)
	}
	return Cmd{Name: "docker", Args: append(args, op...), Dir: st.Dir}
}

// ValidUpdateScript confere que o script (relativo à pasta da stack ou
// absoluto) fica dentro dela depois de resolver links e é um arquivo regular.
// Devolve o caminho absoluto resolvido.
func ValidUpdateScript(stackDir, script string) (string, error) {
	if strings.TrimSpace(script) == "" {
		return "", errors.New("script de atualização vazio")
	}
	real, err := InsideRoot(stackDir, script)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s não é um arquivo regular", real)
	}
	return real, nil
}

func (p *pipeline) local() bool { return isLocalApp(p.app) }

// isLocalApp diz se a app é uma stack compose gerenciada no lugar.
func isLocalApp(a store.App) bool {
	return a.BuildType == "compose-local" || a.SourceType == "local"
}

// StackAction roda start, stop ou restart na stack da app. Não é um deploy: o
// comando e a saída vão para o log do servidor.
func (s *Service) StackAction(ctx context.Context, appID int64, op string) error {
	if op != "start" && op != "stop" && op != "restart" {
		return fmt.Errorf("operação inválida: %q", op)
	}
	if s.isDeleting(appID) {
		return ErrAppDeleting
	}
	app, err := s.DB.GetApp(ctx, appID)
	if err != nil {
		return err
	}
	if !isLocalApp(app) {
		return fmt.Errorf("a aplicação %s não é uma stack compose local", app.Slug)
	}
	st, err := FindStack(ctx, s.Docker, app.ComposeProject)
	if err != nil {
		return err
	}
	cmd := stackCmd(st, op)
	var out bytes.Buffer
	err = s.Runner.Run(ctx, cmd, &out)
	log.Printf("app %d: %s %s (em %s): %s", appID, cmd.Name, strings.Join(cmd.Args, " "), cmd.Dir, strings.TrimSpace(out.String()))
	if err != nil {
		return fmt.Errorf("docker compose %s: %w: %s", op, err, outputTail(out.String()))
	}
	return nil
}

// outputTail devolve o fim da saída de um comando, para mensagens de erro.
func outputTail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxOutputTail {
		return s
	}
	return "…" + strings.ToValidUTF8(s[len(s)-maxOutputTail:], "")
}

// runLocal atualiza a stack no lugar: pull + up -d, ou o script da stack. Sem
// clone, porta, proxy nem limpeza; com sucesso, o deploy vira o atual.
func (p *pipeline) runLocal(ctx context.Context) error {
	s := p.s
	bg := context.WithoutCancel(ctx)
	st, err := FindStack(ctx, s.Docker, p.app.ComposeProject)
	switch {
	case errors.Is(err, ErrStackNotFound):
		return failWith(err, "nenhum container do projeto compose %s encontrado", p.app.ComposeProject)
	case errors.Is(err, ErrStackLabels):
		return failWith(err, "os containers do projeto %s não têm os rótulos de pasta e arquivos do Compose", p.app.ComposeProject)
	case err != nil:
		return fail("falha ao localizar a stack", err)
	}
	p.out.line(fmt.Sprintf("Stack %s em %s", st.Project, st.Dir))
	run := func(c Cmd, what string) error {
		p.out.line("$ " + c.Name + " " + strings.Join(c.Args, " "))
		if err := s.Runner.Run(ctx, c, p.out); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fail(what, err)
		}
		return ctx.Err()
	}
	switch p.app.UpdateMode {
	case "", "pull":
		p.out.step("==> Baixando as imagens")
		if err := run(stackCmd(st, "pull"), "falha no docker compose pull"); err != nil {
			return err
		}
		s.setStatus(bg, p.d.ID, p.d.AppID, "starting", "")
		p.out.step("==> Subindo a stack")
		if err := run(stackCmd(st, "up", "-d"), "falha no docker compose up"); err != nil {
			return err
		}
	case "script":
		// Validado de novo aqui: o link pode ter mudado depois de salvo.
		script, err := ValidUpdateScript(st.Dir, p.app.UpdateScript)
		if err != nil {
			return fail("script de atualização inválido", err)
		}
		s.setStatus(bg, p.d.ID, p.d.AppID, "starting", "")
		p.out.step("==> Rodando o script de atualização")
		if err := run(Cmd{Name: script, Dir: st.Dir}, "falha no script de atualização"); err != nil {
			return err
		}
	default:
		return failWith(nil, "modo de atualização inválido: %s", p.app.UpdateMode)
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
	p.record(bg, "")
	p.markRunning(bg, prev)
	p.out.line("Deploy concluído")
	return nil
}
