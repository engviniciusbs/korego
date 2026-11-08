package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/deploy"
	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/secrets"
	"github.com/engviniciusbs/korego/internal/store"
)

const (
	maxEnvVars      = 200
	maxEnvValue     = 32 << 10
	maxEnvBodyBytes = 8 << 20
	defaultHealthS  = 60
	// cancelWait é quanto a remoção de uma app espera os deploys cancelados
	// terminarem de limpar seus containers.
	cancelWait = 15 * time.Second
)

type appHandlers struct {
	svc     *auth.Service
	db      *store.DB
	deploys *deploy.Service
	dataDir string
	runner  deploy.Runner
	box     *secrets.Box
	github  func(context.Context) bool
}

// AppInput é o corpo de POST/PUT /api/apps.
type AppInput struct {
	ProjectID      int64  `json:"projectId"`
	Slug           string `json:"slug"`
	SourceType     string `json:"sourceType"`
	Repo           string `json:"repo"`
	Image          string `json:"image"`
	BuildType      string `json:"buildType"`
	Branch         string `json:"branch"`
	DockerfilePath string `json:"dockerfilePath"`
	ComposePath    string `json:"composePath"`
	ComposeService string `json:"composeService"`
	InternalPort   int    `json:"internalPort"`
	HealthTimeoutS int    `json:"healthTimeoutS"`
	AutoDeploy     bool   `json:"autoDeploy"`
}

type currentDeployDTO struct {
	ID         int64      `json:"id"`
	Status     string     `json:"status"`
	CommitSHA  string     `json:"commitSha"`
	FinishedAt *time.Time `json:"finishedAt"`
}

// appView é a aplicação na API. Nunca inclui variáveis de ambiente.
type appView struct {
	ID             int64             `json:"id"`
	ProjectID      int64             `json:"projectId"`
	Slug           string            `json:"slug"`
	SourceType     string            `json:"sourceType"`
	Repo           string            `json:"repo"`
	Image          string            `json:"image"`
	BuildType      string            `json:"buildType"`
	Branch         string            `json:"branch"`
	DockerfilePath string            `json:"dockerfilePath"`
	ComposePath    string            `json:"composePath"`
	ComposeService string            `json:"composeService"`
	InternalPort   int               `json:"internalPort"`
	HealthTimeoutS int               `json:"healthTimeoutS"`
	AutoDeploy     bool              `json:"autoDeploy"`
	HostPort       int               `json:"hostPort"`
	CurrentDeploy  *currentDeployDTO `json:"currentDeploy"`
	Domains        []string          `json:"domains"`
	URL            string            `json:"url"`
	CreatedAt      time.Time         `json:"createdAt"`
	UpdatedAt      time.Time         `json:"updatedAt"`
}

func (h appHandlers) view(ctx context.Context, a store.App) (appView, error) {
	v := appView{
		ID: a.ID, ProjectID: a.ProjectID, Slug: a.Slug, SourceType: a.SourceType, Repo: a.Repo,
		Image: a.Image, BuildType: a.BuildType, Branch: a.Branch, DockerfilePath: a.DockerfilePath,
		ComposePath: a.ComposePath, ComposeService: a.ComposeService, InternalPort: a.InternalPort,
		HealthTimeoutS: a.HealthTimeoutS, AutoDeploy: a.AutoDeploy, HostPort: a.HostPort,
		Domains: []string{}, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}
	if a.HostPort > 0 {
		v.URL = "http://127.0.0.1:" + strconv.Itoa(a.HostPort)
	}
	if a.CurrentDeployID != nil {
		d, err := h.db.GetDeploy(ctx, *a.CurrentDeployID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return appView{}, err
		}
		if err == nil {
			v.CurrentDeploy = &currentDeployDTO{ID: d.ID, Status: d.Status, CommitSHA: d.CommitSHA, FinishedAt: d.FinishedAt}
		}
	}
	ds, err := h.db.DomainsByApp(ctx, a.ID)
	if err != nil {
		return appView{}, err
	}
	for _, d := range ds {
		v.Domains = append(v.Domains, d.Hostname)
	}
	return v, nil
}

func appID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	return pathID(w, r, "id", "INVALID_APP_ID", "ID de aplicação inválido.")
}

func appNotFound(w http.ResponseWriter) {
	WriteError(w, http.StatusNotFound, "APP_NOT_FOUND", "Aplicação não encontrada.")
}

// loadApp lê a app do {id}; erros já são respondidos.
func (h appHandlers) loadApp(w http.ResponseWriter, r *http.Request) (store.App, bool) {
	id, ok := appID(w, r)
	if !ok {
		return store.App{}, false
	}
	a, err := h.db.GetApp(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		appNotFound(w)
		return store.App{}, false
	}
	if err != nil {
		internalError(w, err)
		return store.App{}, false
	}
	return a, true
}

// appError é a recusa de validação de uma aplicação.
type appError struct{ field, msg string }

// build valida o corpo e monta a App (sem id, host_port nem deploy atual).
func (h appHandlers) build(ctx context.Context, in AppInput) (store.App, *appError) {
	bad := func(f, m string) (store.App, *appError) { return store.App{}, &appError{f, m} }
	a := store.App{
		ProjectID: in.ProjectID, Slug: in.Slug, SourceType: in.SourceType, BuildType: in.BuildType,
		InternalPort: in.InternalPort, HealthTimeoutS: in.HealthTimeoutS, AutoDeploy: in.AutoDeploy,
	}
	if _, err := h.db.GetProject(ctx, in.ProjectID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return bad("projectId", "Projeto não encontrado.")
		}
		return bad("", err.Error())
	}
	if !deploy.ValidSlug(in.Slug) {
		return bad("slug", "O slug deve ter minúsculas, números e hífens (até 63 caracteres).")
	}
	switch in.SourceType {
	case "github", "git", "image":
	default:
		return bad("sourceType", "Origem inválida: use github, git ou image.")
	}
	switch in.BuildType {
	case "dockerfile", "compose", "railpack", "image":
	default:
		return bad("buildType", "Tipo de build inválido: use dockerfile, compose, railpack ou image.")
	}
	switch in.SourceType {
	case "image":
		if in.BuildType != "image" {
			return bad("buildType", "Aplicações de imagem usam o tipo de build image.")
		}
		if !deploy.ValidImageRef(in.Image) {
			return bad("image", "Informe uma imagem válida, como nginx:1-alpine.")
		}
		a.Image = in.Image
	default:
		if in.BuildType == "image" {
			return bad("buildType", "O tipo de build image só vale para origem image.")
		}
		if in.SourceType == "github" && !deploy.ValidRepo(in.Repo) {
			return bad("repo", "Informe o repositório no formato dono/nome.")
		}
		if in.SourceType == "git" && !deploy.ValidGitURL(in.Repo) {
			return bad("repo", "Informe uma URL https válida para o repositório.")
		}
		if !deploy.ValidBranch(in.Branch) {
			return bad("branch", "Informe um branch válido.")
		}
		a.Repo, a.Branch = in.Repo, in.Branch
		a.DockerfilePath = in.DockerfilePath
		if a.DockerfilePath == "" {
			a.DockerfilePath = "Dockerfile"
		}
		if !deploy.ValidRelPath(a.DockerfilePath) {
			return bad("dockerfilePath", "Caminho do Dockerfile inválido.")
		}
		if in.BuildType == "compose" {
			a.ComposePath = in.ComposePath
			if a.ComposePath == "" {
				a.ComposePath = "docker-compose.yml"
			}
			if !deploy.ValidRelPath(a.ComposePath) {
				return bad("composePath", "Caminho do arquivo Compose inválido.")
			}
			if !deploy.ValidServiceName(in.ComposeService) {
				return bad("composeService", "Informe o nome do serviço do Compose que recebe o tráfego.")
			}
			a.ComposeService = in.ComposeService
		}
	}
	if !deploy.ValidPort(in.InternalPort) {
		return bad("internalPort", "Informe a porta interna da aplicação (1 a 65535).")
	}
	if in.HealthTimeoutS != 0 && (in.HealthTimeoutS < 10 || in.HealthTimeoutS > 600) {
		return bad("healthTimeoutS", "O tempo de verificação de saúde deve ser 0 (padrão) ou de 10 a 600 segundos.")
	}
	if a.HealthTimeoutS == 0 {
		a.HealthTimeoutS = defaultHealthS
	}
	return a, nil
}

// list serves GET /api/apps?project=<id>.
func (h appHandlers) list(w http.ResponseWriter, r *http.Request) {
	var pid *int64
	if q := r.URL.Query().Get("project"); q != "" {
		n, err := strconv.ParseInt(q, 10, 64)
		if err != nil || n <= 0 {
			WriteError(w, http.StatusBadRequest, "INVALID_PROJECT_ID", "ID de projeto inválido.")
			return
		}
		pid = &n
	}
	apps, err := h.db.ListApps(r.Context(), pid)
	if err != nil {
		internalError(w, err)
		return
	}
	out := make([]appView, 0, len(apps))
	for _, a := range apps {
		v, err := h.view(r.Context(), a)
		if err != nil {
			internalError(w, err)
			return
		}
		out = append(out, v)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"apps": out})
}

// get serves GET /api/apps/{id}.
func (h appHandlers) get(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadApp(w, r)
	if !ok {
		return
	}
	v, err := h.view(r.Context(), a)
	if err != nil {
		internalError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"app": v})
}

// create serves POST /api/apps.
func (h appHandlers) create(w http.ResponseWriter, r *http.Request) {
	var in AppInput
	if !decodeJSON(w, r, &in) {
		return
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	a, ae := h.build(ctx, in)
	if ae != nil {
		record(h.svc, r, "app.create", in.Slug, map[string]any{"ok": false})
		if ae.field == "" {
			internalError(w, errors.New(ae.msg))
			return
		}
		writeFieldError(w, http.StatusBadRequest, "INVALID_APP", ae.msg, ae.field)
		return
	}
	if a.SourceType == "github" && (h.github == nil || !h.github(ctx)) {
		record(h.svc, r, "app.create", a.Slug, map[string]any{"ok": false})
		WriteError(w, http.StatusConflict, "NO_GITHUB", "Conecte o GitHub antes de criar uma aplicação com origem GitHub.")
		return
	}
	if err := h.db.InsertApp(ctx, &a); err != nil {
		record(h.svc, r, "app.create", a.Slug, map[string]any{"ok": false})
		if errors.Is(err, store.ErrConflict) {
			WriteError(w, http.StatusConflict, "APP_EXISTS", "Já existe uma aplicação com esse slug.")
			return
		}
		internalError(w, err)
		return
	}
	record(h.svc, r, "app.create", a.Slug, map[string]any{"ok": true})
	v, err := h.view(ctx, a)
	if err != nil {
		internalError(w, err)
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]any{"app": v})
}

// update serves PUT /api/apps/{id}. O slug é imutável.
func (h appHandlers) update(w http.ResponseWriter, r *http.Request) {
	var in AppInput
	if !decodeJSON(w, r, &in) {
		return
	}
	cur, ok := h.loadApp(w, r)
	if !ok {
		return
	}
	if in.Slug == "" {
		in.Slug = cur.Slug
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	if in.Slug != cur.Slug {
		record(h.svc, r, "app.update", cur.Slug, map[string]any{"ok": false})
		writeFieldError(w, http.StatusBadRequest, "INVALID_APP", "O slug não pode ser alterado.", "slug")
		return
	}
	a, ae := h.build(ctx, in)
	if ae != nil {
		record(h.svc, r, "app.update", cur.Slug, map[string]any{"ok": false})
		if ae.field == "" {
			internalError(w, errors.New(ae.msg))
			return
		}
		writeFieldError(w, http.StatusBadRequest, "INVALID_APP", ae.msg, ae.field)
		return
	}
	a.ID = cur.ID
	if err := h.db.UpdateAppConfig(ctx, &a); err != nil {
		record(h.svc, r, "app.update", cur.Slug, map[string]any{"ok": false})
		if errors.Is(err, store.ErrNotFound) {
			appNotFound(w)
			return
		}
		internalError(w, err)
		return
	}
	record(h.svc, r, "app.update", cur.Slug, map[string]any{"ok": true})
	fresh, err := h.db.GetApp(ctx, cur.ID)
	if err != nil {
		internalError(w, err)
		return
	}
	v, err := h.view(ctx, fresh)
	if err != nil {
		internalError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"app": v})
}

func terminalStatus(s string) bool {
	return s == "running" || s == "failed" || s == "superseded" || s == "cancelled"
}

// cancelActive cancela os deploys ativos da app e espera terminarem.
func (h appHandlers) cancelActive(ctx context.Context, appID int64) error {
	ds, err := h.db.ListDeploys(ctx, appID, 1000)
	if err != nil {
		return err
	}
	var waiting []int64
	for _, d := range ds {
		if terminalStatus(d.Status) {
			continue
		}
		if err := h.deploys.Cancel(ctx, d.ID); err != nil && !errors.Is(err, deploy.ErrDeployFinished) {
			return err
		}
		waiting = append(waiting, d.ID)
	}
	deadline := time.Now().Add(cancelWait)
	for _, id := range waiting {
		for {
			d, err := h.db.GetDeploy(ctx, id)
			if err != nil || terminalStatus(d.Status) || time.Now().After(deadline) {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	return nil
}

// teardown remove containers, imagens e o diretório persistente da app.
func (h appHandlers) teardown(ctx context.Context, a store.App) error {
	if err := h.cancelActive(ctx, a.ID); err != nil {
		return err
	}
	dir := deploy.AppDir(h.dataDir, a.Slug)
	if a.BuildType == "compose" {
		if _, err := os.Stat(dir); err == nil {
			runner := h.runner
			if runner == nil {
				runner = deploy.ExecRunner{}
			}
			err := runner.Run(ctx, deploy.Cmd{Name: "docker", Dir: dir,
				Args: []string{"compose", "-p", "korego-" + a.Slug, "down", "--remove-orphans"}}, io.Discard)
			if err != nil {
				return err
			}
		}
	} else {
		if err := h.removeLabeled(ctx, a.Slug); err != nil {
			return err
		}
	}
	// Segunda varredura: pega o que um deploy em curso criou durante a remoção
	// (inclusive containers Compose, que carregam korego.app pelo override).
	if err := h.removeLabeled(ctx, a.Slug); err != nil {
		return err
	}
	if err := deploy.Cleanup(ctx, h.deploys.Docker, a, nil, io.Discard); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// removeLabeled remove à força todo container com korego.app=<slug>.
func (h appHandlers) removeLabeled(ctx context.Context, slug string) error {
	cs, err := h.deploys.Docker.ListByLabel(ctx, "korego.app", slug)
	if err != nil {
		return err
	}
	for _, c := range cs {
		if err := h.deploys.Docker.RemoveForce(ctx, c.ID); err != nil && !errors.Is(err, docker.ErrNotFound) {
			return err
		}
	}
	return nil
}

// remove serves DELETE /api/apps/{id}.
func (h appHandlers) remove(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadApp(w, r)
	if !ok {
		return
	}
	ctx, cancel := detached(r, 5*time.Minute)
	defer cancel()
	defer h.deploys.BeginDelete(a.ID)()
	ds, err := h.db.DomainsByApp(ctx, a.ID)
	if err == nil && len(ds) > 0 {
		record(h.svc, r, "app.delete", a.Slug, map[string]any{"ok": false})
		WriteError(w, http.StatusConflict, "APP_HAS_DOMAINS", "Remova os domínios da aplicação antes de apagá-la.")
		return
	}
	if err == nil {
		err = h.teardown(ctx, a)
	}
	if err == nil {
		err = h.db.DeleteApp(ctx, a.ID)
	}
	switch {
	case errors.Is(err, store.ErrConflict):
		record(h.svc, r, "app.delete", a.Slug, map[string]any{"ok": false})
		WriteError(w, http.StatusConflict, "APP_HAS_DOMAINS", "Remova os domínios da aplicação antes de apagá-la.")
	case errors.Is(err, store.ErrNotFound):
		record(h.svc, r, "app.delete", a.Slug, map[string]any{"ok": false})
		appNotFound(w)
	case err != nil:
		record(h.svc, r, "app.delete", a.Slug, map[string]any{"ok": false})
		internalError(w, err)
	default:
		record(h.svc, r, "app.delete", a.Slug, map[string]any{"ok": true})
		w.WriteHeader(http.StatusNoContent)
	}
}

// capNames limita a lista da auditoria aos primeiros 50 nomes.
func capNames(n []string) []string {
	if len(n) > 50 {
		return n[:50]
	}
	return n
}

// envNames serves GET /api/apps/{id}/env: só os nomes.
func (h appHandlers) envNames(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadApp(w, r)
	if !ok {
		return
	}
	names, err := h.db.AppEnvNames(r.Context(), a.ID)
	if err != nil {
		internalError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"names": names})
}

type envInput struct {
	Set    map[string]string `json:"set"`
	Remove []string          `json:"remove"`
}

func envInvalid(w http.ResponseWriter, msg, field string) {
	writeFieldError(w, http.StatusBadRequest, "INVALID_ENV", msg, field)
}

// envSet serves PUT /api/apps/{id}/env.
func (h appHandlers) envSet(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadApp(w, r)
	if !ok {
		return
	}
	var in envInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxEnvBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || dec.More() {
		WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Corpo da requisição inválido.")
		return
	}
	setNames := make([]string, 0, len(in.Set))
	for n := range in.Set {
		setNames = append(setNames, n)
	}
	sort.Strings(setNames)
	remove := slices.Clone(in.Remove)
	sort.Strings(remove)
	remove = slices.Compact(remove)
	audit := func(ok bool) {
		record(h.svc, r, "app.env", a.Slug, map[string]any{
			"set": capNames(setNames), "remove": capNames(remove),
			"setCount": len(setNames), "removeCount": len(remove), "ok": ok})
	}
	if bad := h.checkEnv(r.Context(), a.ID, in.Set, setNames, remove); bad != nil {
		// Nomes ainda não validados: a auditoria leva só as contagens.
		record(h.svc, r, "app.env", a.Slug, map[string]any{
			"setCount": len(setNames), "removeCount": len(remove), "ok": false})
		envInvalid(w, bad.msg, bad.field)
		return
	}
	enc := make(map[string]string, len(in.Set))
	for _, n := range setNames {
		c, err := h.box.Encrypt([]byte(in.Set[n]))
		if err != nil {
			audit(false)
			internalError(w, err)
			return
		}
		enc[n] = c
	}
	ctx, cancel := detached(r, mutationTimeout)
	defer cancel()
	if err := h.db.SetAppEnv(ctx, a.ID, enc, remove); err != nil {
		audit(false)
		internalError(w, err)
		return
	}
	audit(true)
	w.WriteHeader(http.StatusNoContent)
}

func (h appHandlers) checkEnv(ctx context.Context, appID int64, set map[string]string, setNames, remove []string) *appError {
	for _, n := range setNames {
		if !deploy.ValidEnvName(n) {
			return &appError{n, "Nome de variável inválido: " + n}
		}
		if len(set[n]) > maxEnvValue {
			return &appError{n, "O valor da variável excede 32 KiB."}
		}
	}
	for _, n := range remove {
		if !deploy.ValidEnvName(n) {
			return &appError{n, "Nome de variável inválido: " + n}
		}
		if _, dup := set[n]; dup {
			return &appError{n, "A variável não pode ser definida e removida na mesma requisição."}
		}
	}
	cur, err := h.db.AppEnvNames(ctx, appID)
	if err != nil {
		log.Printf("env: ler nomes: %v", err)
		return nil // a gravação falha adiante se o banco estiver ruim
	}
	total := map[string]bool{}
	for _, n := range cur {
		total[n] = true
	}
	for _, n := range remove {
		delete(total, n)
	}
	for _, n := range setNames {
		total[n] = true
	}
	if len(total) > maxEnvVars {
		return &appError{"set", "Uma aplicação pode ter no máximo 200 variáveis."}
	}
	return nil
}
