package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/engviniciusbs/korego/internal/audit"
	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/deploy"
	"github.com/engviniciusbs/korego/internal/store"
)

// stackActionTimeout limita start/stop/restart de uma stack. O comando roda
// desligado da requisição: um cliente que cai não interrompe o compose.
const stackActionTimeout = 5 * time.Minute

// adoptMu serializa as adoções: a checagem "já adotada" e a criação da app
// não podem se cruzar (publish_root e compose_project não têm índice único).
var adoptMu sync.Mutex

// composeProjectRe segue os nomes de projeto aceitos pelo Compose.
var composeProjectRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,254}$`)

// unsafeStackDirs são pastas do sistema que nunca servem de pasta de stack.
var unsafeStackDirs = map[string]bool{
	"/": true, "/etc": true, "/usr": true, "/bin": true, "/sbin": true, "/lib": true, "/lib64": true,
	"/boot": true, "/proc": true, "/sys": true, "/dev": true, "/root": true, "/var": true, "/home": true,
}

// safeStackDir recusa como pasta de stack uma pasta do sistema, também depois
// de limpar o caminho e seguir links; uma pasta que não resolve é recusada.
func safeStackDir(dir string) bool {
	if unsafeStackDirs[filepath.ToSlash(filepath.Clean(dir))] {
		return false
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	return !unsafeStackDirs[filepath.ToSlash(filepath.Clean(real))]
}

// requireCode confere o 2FA de uma ação que só às vezes o exige. Por token,
// o cabeçalho X-Korego-Code, exatamente como o gate; por sessão, o code do
// corpo, como confirmCode. Erros já são respondidos.
func (h appHandlers) requireCode(w http.ResponseWriter, r *http.Request, bodyCode, action, target string) bool {
	if codeConfirmed(r.Context()) {
		return true
	}
	if _, isToken := audit.TokenFrom(r.Context()); isToken {
		passed := false
		requireCodeForToken(h.svc, h.lim)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			passed = true
		})).ServeHTTP(w, r)
		return passed
	}
	sess, ok := CurrentSession(r.Context())
	if !ok {
		unauthorized(w)
		return false
	}
	rec := func(meta map[string]any) { record(h.svc, r, action, target, meta) }
	return verifyCode(h.svc, w, r, sess.UserID, auth.ClientIP(r), bodyCode, rec)
}

// stackError responde as falhas ao localizar uma stack compose.
func stackError(w http.ResponseWriter, project string, err error) {
	switch {
	case errors.Is(err, deploy.ErrStackNotFound):
		WriteError(w, http.StatusUnprocessableEntity, "STACK_NOT_FOUND",
			"Nenhum container do projeto compose "+project+" foi encontrado, nem parado. Suba a stack na VPS e tente de novo.")
	case errors.Is(err, deploy.ErrStackLabels):
		WriteError(w, http.StatusUnprocessableEntity, "STACK_LABELS",
			"Os containers do projeto compose "+project+" não têm os rótulos de pasta e arquivos do Compose; a stack precisa ter sido criada com docker compose.")
	default:
		internalError(w, err)
	}
}

// checkUpdateScript confere, no modo script, que o script fica dentro da
// pasta atual da stack (depois de seguir links). Erros já são respondidos e
// auditados com action.
func (h appHandlers) checkUpdateScript(ctx context.Context, w http.ResponseWriter, r *http.Request,
	action, target string, a store.App) bool {
	if a.UpdateMode != "script" {
		return true
	}
	st, err := deploy.FindStack(ctx, h.deploys.Docker, a.ComposeProject)
	if err != nil {
		record(h.svc, r, action, target, map[string]any{"ok": false})
		stackError(w, a.ComposeProject, err)
		return false
	}
	if _, err := deploy.ValidUpdateScript(st.Dir, a.UpdateScript); err != nil {
		record(h.svc, r, action, target, map[string]any{"ok": false})
		writeFieldError(w, http.StatusBadRequest, "INVALID_APP",
			"O script precisa ser um arquivo dentro da pasta da stack ("+st.Dir+"): "+err.Error(), "updateScript")
		return false
	}
	return true
}

// adoptStaticInput é o corpo de POST /api/adopt/static. current é o link
// servido hoje, como /var/www/site/current. staticSpa ausente copia o SPA da
// rota estática adotada.
type adoptStaticInput struct {
	ProjectID             int64  `json:"projectId"`
	Slug                  string `json:"slug"`
	Current               string `json:"current"`
	SourceType            string `json:"sourceType"`
	Repo                  string `json:"repo"`
	Branch                string `json:"branch"`
	StaticBuildCmd        string `json:"staticBuildCmd"`
	StaticOutputDir       string `json:"staticOutputDir"`
	StaticBaseDir         string `json:"staticBaseDir"`
	StaticImage           string `json:"staticImage"`
	StaticSPA             *bool  `json:"staticSpa"`
	StaticImmutablePrefix string `json:"staticImmutablePrefix"`
}

// adoptedDomain é um domínio com rotas estáticas trocadas para a app adotada.
type adoptedDomain struct {
	orig, next store.Domain
	route      store.DomainRoute // a primeira rota trocada (antes da troca)
}

// sameRoot diz se a pasta de uma rota estática é o current adotado. Uma rota
// que aponta direto para uma versão (releases/<x>) fica como está: ela não
// acompanha os deploys e trocá-la mudaria o que ela serve.
func sameRoot(root, current string) bool {
	return root != "" && filepath.Clean(root) == current
}

// adoptedDomains acha os domínios com rota estática para o current adotado e
// monta a versão com essas rotas como "app" sem AppID: store.AdoptStatic as
// aponta para a app criada (as demais rotas ficam como estão).
func adoptedDomains(list []store.Domain, current string) []adoptedDomain {
	var out []adoptedDomain
	for _, d := range list {
		if d.IsPanel {
			continue
		}
		next := d
		next.Routes = make([]store.DomainRoute, len(d.Routes))
		var first *store.DomainRoute
		for i, rt := range d.Routes {
			if rt.TargetType == "static" && sameRoot(rt.Root, current) {
				if first == nil {
					c := rt
					first = &c
				}
				rt = store.DomainRoute{ID: rt.ID, DomainID: rt.DomainID, Position: rt.Position, Path: rt.Path,
					TargetType: "app"}
			}
			next.Routes[i] = rt
		}
		if first != nil {
			out = append(out, adoptedDomain{orig: d, next: next, route: *first})
		}
	}
	return out
}

func (h appHandlers) wwwBase() string {
	if h.wwwRoot != "" {
		return h.wwwRoot
	}
	return deploy.WWWRoot
}

// lexicallyUnder diz se p fica em uma subpasta de root, sem resolver links.
func lexicallyUnder(root, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// staticRootTaken diz se alguma app estática já publica em root.
func (h appHandlers) staticRootTaken(ctx context.Context, root string) (bool, error) {
	apps, err := h.db.ListApps(ctx, nil)
	if err != nil {
		return false, err
	}
	for _, a := range apps {
		if a.BuildType != "static" {
			continue
		}
		p := a.PublishRoot
		if p == "" {
			p = filepath.Join(h.wwwBase(), a.Slug)
		}
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		if filepath.Clean(p) == root {
			return true, nil
		}
	}
	return false, nil
}

// adoptStatic serves POST /api/adopt/static: assume no lugar um site estático
// publicado em <raiz>/current → <raiz>/releases/<versão>. Cria a app (sem
// deploy automático), um deploy "adopt" apontando para a versão no ar e troca
// para a app as rotas estáticas que servem esse current. Nada muda no disco.
func (h appHandlers) adoptStatic(w http.ResponseWriter, r *http.Request) {
	var in adoptStaticInput
	if !decodeJSON(w, r, &in) {
		return
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	fail := func(meta map[string]any) {
		meta["ok"], meta["kind"] = false, "static"
		record(h.svc, r, "app.adopted", in.Slug, meta)
	}
	spa := in.StaticSPA != nil && *in.StaticSPA
	a, ae := h.build(ctx, AppInput{
		ProjectID: in.ProjectID, Slug: in.Slug, SourceType: in.SourceType, Repo: in.Repo, Branch: in.Branch,
		BuildType: "static", StaticBuildCmd: in.StaticBuildCmd, StaticOutputDir: in.StaticOutputDir,
		StaticBaseDir: in.StaticBaseDir, StaticImage: in.StaticImage, StaticSPA: spa,
		StaticImmutablePrefix: in.StaticImmutablePrefix,
	})
	if ae != nil {
		fail(map[string]any{})
		if ae.field == "" {
			internalError(w, errors.New(ae.msg))
			return
		}
		writeFieldError(w, http.StatusBadRequest, "INVALID_APP", ae.msg, ae.field)
		return
	}
	if a.SourceType == "github" && (h.github == nil || !h.github(ctx)) {
		fail(map[string]any{})
		WriteError(w, http.StatusConflict, "NO_GITHUB", "Conecte o GitHub antes de adotar um site com origem GitHub.")
		return
	}
	current := filepath.Clean(strings.TrimSpace(in.Current))
	publishRoot, releaseDir, err := deploy.ValidateAdoptRootIn(h.wwwRoot, current)
	if err != nil {
		fail(map[string]any{"stage": "layout"})
		writeFieldError(w, http.StatusUnprocessableEntity, "INVALID_LAYOUT",
			"O site precisa estar em <pasta>/current, um link para <pasta>/releases/<versão>, dentro de "+
				h.wwwBase()+": "+err.Error(), "current")
		return
	}

	adoptMu.Lock()
	defer adoptMu.Unlock()
	if taken, err := h.staticRootTaken(ctx, publishRoot); err != nil {
		fail(map[string]any{"stage": "conflict"})
		internalError(w, err)
		return
	} else if taken {
		fail(map[string]any{"stage": "conflict"})
		WriteError(w, http.StatusConflict, "ALREADY_ADOPTED", "A pasta "+publishRoot+" já pertence a uma aplicação do KoreGO.")
		return
	}
	list, err := h.db.ListDomains(ctx)
	if err != nil {
		fail(map[string]any{"stage": "domains"})
		internalError(w, err)
		return
	}
	if doms := adoptedDomains(list, current); len(doms) > 0 {
		if in.StaticSPA == nil {
			a.StaticSPA = doms[0].route.SPA
		}
		if in.StaticImmutablePrefix == "" {
			a.StaticImmutablePrefix = doms[0].route.ImmutablePrefix
		}
	}
	// Grava a raiz como o usuário a vê (dentro da raiz dos sites, sem
	// resolver links): o proxy só aceita pastas sob /var/www e o Caddy a
	// enxerga por esse caminho, mesmo com /var/www sendo um link.
	stored := publishRoot
	if lex := filepath.Dir(current); lex != publishRoot && lexicallyUnder(h.wwwBase(), lex) {
		stored = lex
	}
	a.PublishRoot, a.AutoDeploy = stored, false
	doms := adoptedDomains(list, current)
	next := make([]store.Domain, 0, len(doms))
	hosts := make([]string, 0, len(doms))
	for _, ad := range doms {
		next = append(next, ad.next)
		hosts = append(hosts, ad.orig.Hostname)
	}
	dep := store.Deploy{Trigger: "adopt", Status: "running", ReleaseDir: releaseDir,
		CommitSHA: deploy.ReleaseSHA(filepath.Base(releaseDir)), CommitMessage: "Site adotado da VPS"}
	if err := h.db.AdoptStatic(ctx, &a, &dep, next); err != nil {
		fail(map[string]any{"stage": "save"})
		if errors.Is(err, store.ErrConflict) {
			WriteError(w, http.StatusConflict, "APP_EXISTS", "Já existe uma aplicação com esse slug.")
			return
		}
		internalError(w, err)
		return
	}
	if h.dom != nil {
		if err := h.dom.ReapplyApp(ctx, a.ID); err != nil {
			// Gravado: a próxima reconciliação aplica. O Caddy segue servindo
			// o mesmo current até lá.
			log.Printf("adoção de %s: reaplicar proxy: %v", a.Slug, err)
		}
	}
	record(h.svc, r, "app.adopted", a.Slug, map[string]any{"ok": true, "kind": "static",
		"publishRoot": publishRoot, "release": filepath.Base(releaseDir), "domains": hosts})
	fresh, err := h.db.GetApp(ctx, a.ID)
	if err != nil {
		internalError(w, err)
		return
	}
	v, err := h.view(ctx, fresh)
	if err != nil {
		internalError(w, err)
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"app": v, "domains": hosts})
}

// AdoptComposeInput é o corpo de POST /api/adopt/compose (sem o código).
type AdoptComposeInput struct {
	ProjectID      int64  `json:"projectId"`
	Slug           string `json:"slug"`
	ComposeProject string `json:"composeProject"`
	UpdateMode     string `json:"updateMode"`
	UpdateScript   string `json:"updateScript"`
}

// adoptComposeBody acrescenta o código 2FA da sessão (modo script).
type adoptComposeBody struct {
	AdoptComposeInput
	Code string `json:"code"`
}

// adoptCompose serves POST /api/adopt/compose: passa a gerenciar no lugar uma
// stack compose que já roda na VPS. Nada é recriado nem gravado na stack. O
// modo script exige o código 2FA (o script roda como root).
func (h appHandlers) adoptCompose(w http.ResponseWriter, r *http.Request) {
	var body adoptComposeBody
	if !decodeJSON(w, r, &body) {
		return
	}
	in := body.AdoptComposeInput
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	fail := func(meta map[string]any) {
		meta["ok"], meta["kind"] = false, "compose"
		record(h.svc, r, "app.adopted", in.Slug, meta)
	}
	a, ae := h.build(ctx, AppInput{ProjectID: in.ProjectID, Slug: in.Slug, SourceType: "local",
		BuildType: "compose-local", UpdateMode: in.UpdateMode, UpdateScript: in.UpdateScript})
	if ae != nil {
		fail(map[string]any{})
		if ae.field == "" {
			internalError(w, errors.New(ae.msg))
			return
		}
		writeFieldError(w, http.StatusBadRequest, "INVALID_APP", ae.msg, ae.field)
		return
	}
	project := strings.TrimSpace(in.ComposeProject)
	if !composeProjectRe.MatchString(project) {
		fail(map[string]any{})
		writeFieldError(w, http.StatusBadRequest, "INVALID_APP", "Informe o nome do projeto compose.", "composeProject")
		return
	}
	a.ComposeProject = project

	adoptMu.Lock()
	defer adoptMu.Unlock()
	switch _, err := h.db.AppByComposeProject(ctx, project); {
	case err == nil:
		fail(map[string]any{"stage": "conflict"})
		WriteError(w, http.StatusConflict, "ALREADY_ADOPTED", "O projeto compose "+project+" já pertence a uma aplicação do KoreGO.")
		return
	case !errors.Is(err, store.ErrNotFound):
		internalError(w, err)
		return
	}
	st, err := deploy.FindStack(ctx, h.deploys.Docker, project)
	if err != nil {
		fail(map[string]any{"stage": "stack"})
		stackError(w, project, err)
		return
	}
	if !safeStackDir(st.Dir) {
		fail(map[string]any{"stage": "stack"})
		WriteError(w, http.StatusUnprocessableEntity, "STACK_DIR_UNSAFE",
			"A pasta da stack "+project+" é "+st.Dir+", uma pasta do sistema ou inexistente. Mova a stack para uma pasta própria antes de adotá-la.")
		return
	}
	if !h.checkUpdateScript(ctx, w, r, "app.adopted", in.Slug, a) {
		return
	}
	if a.UpdateMode == "script" && !h.requireCode(w, r, body.Code, "app.adopted", in.Slug) {
		return
	}
	if err := h.db.InsertApp(ctx, &a); err != nil {
		fail(map[string]any{})
		if errors.Is(err, store.ErrConflict) {
			WriteError(w, http.StatusConflict, "APP_EXISTS", "Já existe uma aplicação com esse slug.")
			return
		}
		internalError(w, err)
		return
	}
	record(h.svc, r, "app.adopted", a.Slug, map[string]any{"ok": true, "kind": "compose",
		"composeProject": project, "updateMode": a.UpdateMode})
	v, err := h.view(ctx, a)
	if err != nil {
		internalError(w, err)
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"app": v})
}

// stackAction serves POST /api/apps/{id}/stack/{op} (start, stop, restart)
// de uma stack compose-local. O stop exige o código 2FA ({code} pela sessão,
// X-Korego-Code pelo token).
func (h appHandlers) stackAction(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadApp(w, r)
	if !ok {
		return
	}
	op := chi.URLParam(r, "op")
	if op != "start" && op != "stop" && op != "restart" {
		WriteError(w, http.StatusBadRequest, "INVALID_OP", "Operação inválida: use start, stop ou restart.")
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
		return
	}
	rec := func(ok bool) {
		record(h.svc, r, "app.stack_action", a.Slug, map[string]any{"op": op, "ok": ok})
	}
	if a.SourceType != "local" {
		rec(false)
		WriteError(w, http.StatusConflict, "NOT_COMPOSE_LOCAL", "Só as stacks compose da VPS têm iniciar, parar e reiniciar.")
		return
	}
	ctx, cancel := detached(r, stackActionTimeout)
	defer cancel()
	if op == "stop" {
		// Recusa barata antes de gastar o código.
		if _, err := deploy.FindStack(ctx, h.deploys.Docker, a.ComposeProject); err != nil {
			rec(false)
			stackError(w, a.ComposeProject, err)
			return
		}
		if !h.requireCode(w, r, in.Code, "app.stack_action", a.Slug) {
			return
		}
	}
	err := h.deploys.StackAction(ctx, a.ID, op)
	switch {
	case err == nil:
		rec(true)
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, deploy.ErrStackNotFound), errors.Is(err, deploy.ErrStackLabels):
		rec(false)
		stackError(w, a.ComposeProject, err)
	case errors.Is(err, deploy.ErrAppDeleting):
		rec(false)
		WriteError(w, http.StatusConflict, "APP_DELETING", "A aplicação está sendo removida.")
	default:
		rec(false)
		log.Printf("app %s: stack %s: %v", a.Slug, op, err)
		WriteError(w, http.StatusBadGateway, "STACK_ACTION_FAILED",
			"O docker compose falhou ao executar "+op+"; veja o log do servidor.")
	}
}

// stackInfo serves GET /api/apps/{id}/stack: pasta e arquivos compose de uma
// stack compose-local, lidos na hora dos rótulos do container mais recente.
func (h appHandlers) stackInfo(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadApp(w, r)
	if !ok {
		return
	}
	if a.SourceType != "local" {
		WriteError(w, http.StatusConflict, "NOT_COMPOSE_LOCAL", "Só as stacks compose da VPS têm pasta de stack.")
		return
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	st, err := deploy.FindStack(ctx, h.deploys.Docker, a.ComposeProject)
	if err != nil {
		stackError(w, a.ComposeProject, err)
		return
	}
	files := st.Files
	if files == nil {
		files = []string{}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"project": st.Project, "dir": st.Dir, "files": files,
		"envPath": deploy.StackEnvPath(st)})
}

// stackEnvPath acha o .env da stack e recusa um .env que, por link, sai da
// pasta da stack.
func (h appHandlers) stackEnvPath(w http.ResponseWriter, r *http.Request, a store.App) (string, bool) {
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	st, err := deploy.FindStack(ctx, h.deploys.Docker, a.ComposeProject)
	if err != nil {
		stackError(w, a.ComposeProject, err)
		return "", false
	}
	p := deploy.StackEnvPath(st)
	if _, err := os.Lstat(p); err == nil {
		if _, err := deploy.InsideRoot(st.Dir, p); err != nil {
			WriteError(w, http.StatusUnprocessableEntity, "ENV_OUTSIDE_STACK",
				"O .env da stack aponta para fora da pasta da stack ("+st.Dir+").")
			return "", false
		}
	}
	return p, true
}

// localEnvNames: GET /api/apps/{id}/env de uma stack compose-local lê os nomes
// do .env da pasta da stack (nunca os valores).
func (h appHandlers) localEnvNames(w http.ResponseWriter, r *http.Request, a store.App) {
	p, ok := h.stackEnvPath(w, r, a)
	if !ok {
		return
	}
	names, err := deploy.EnvNames(p)
	if errors.Is(err, os.ErrNotExist) {
		names, err = []string{}, nil
	}
	if err != nil {
		internalError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"names": names})
}

// localEnvSet: PUT /api/apps/{id}/env de uma stack compose-local regrava o
// .env da stack (atômico, mantendo o resto do arquivo). Não reinicia nada e
// não toca na tabela de variáveis do KoreGO.
func (h appHandlers) localEnvSet(w http.ResponseWriter, r *http.Request, a store.App, in envInput,
	setNames, remove []string) {
	audit := func(ok bool) {
		record(h.svc, r, "app.env", a.Slug, map[string]any{
			"set": capNames(setNames), "setCount": len(setNames), "file": ".env", "ok": ok})
	}
	if len(remove) > 0 {
		record(h.svc, r, "app.env", a.Slug, map[string]any{"setCount": len(setNames), "file": ".env", "ok": false})
		envInvalid(w, "Remover variáveis do .env da stack não é suportado; edite o arquivo na VPS.", "remove")
		return
	}
	for _, n := range setNames {
		msg := ""
		switch v := in.Set[n]; {
		case !deploy.ValidEnvName(n):
			msg = "Nome de variável inválido: " + n
		case len(v) > maxEnvValue:
			msg = "O valor da variável excede 32 KiB."
		case strings.ContainsAny(v, "\r\n"):
			msg = "O valor de " + n + " não pode ter quebra de linha."
		}
		if msg != "" {
			record(h.svc, r, "app.env", a.Slug, map[string]any{"setCount": len(setNames), "file": ".env", "ok": false})
			envInvalid(w, msg, n)
			return
		}
	}
	p, ok := h.stackEnvPath(w, r, a)
	if !ok {
		audit(false)
		return
	}
	if len(setNames) > 0 {
		if err := deploy.SetEnv(p, in.Set); err != nil {
			audit(false)
			internalError(w, err)
			return
		}
	}
	audit(true)
	w.WriteHeader(http.StatusNoContent)
}
