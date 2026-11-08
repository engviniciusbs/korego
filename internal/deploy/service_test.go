package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/jobs"
	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/docker/dockertest"
	"github.com/engviniciusbs/korego/internal/secrets"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recorder guarda, em ordem, o que os falsos viram.
type recorder struct {
	mu sync.Mutex
	ev []string
}

func (r *recorder) add(format string, a ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ev = append(r.ev, fmt.Sprintf(format, a...))
}

func (r *recorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.ev)
}

// first devolve o índice do primeiro evento com o prefixo, ou -1.
func (r *recorder) first(prefix string) int {
	for i, e := range r.list() {
		if strings.HasPrefix(e, prefix) {
			return i
		}
	}
	return -1
}

// last devolve o índice do último evento com o prefixo, ou -1.
func (r *recorder) last(prefix string) int {
	l := r.list()
	for i := len(l) - 1; i >= 0; i-- {
		if strings.HasPrefix(l[i], prefix) {
			return i
		}
	}
	return -1
}

func (r *recorder) count(prefix string) int {
	n := 0
	for _, e := range r.list() {
		if strings.HasPrefix(e, prefix) {
			n++
		}
	}
	return n
}

// fakeRouter registra a porta da app no momento de cada ReapplyApp.
type fakeRouter struct {
	db    *store.DB
	rec   *recorder
	errFn func(n int) error
	mu    sync.Mutex
	n     int
}

func (f *fakeRouter) ReapplyApp(ctx context.Context, appID int64) error {
	f.mu.Lock()
	n := f.n
	f.n++
	f.mu.Unlock()
	a, err := f.db.GetApp(context.WithoutCancel(ctx), appID)
	if err != nil {
		return err
	}
	f.rec.add("reapply:%d", a.HostPort)
	if f.errFn != nil {
		return f.errFn(n)
	}
	return nil
}

// scriptRunner simula git e docker; hook (opcional) decide antes do padrão.
type scriptRunner struct {
	mu   sync.Mutex
	cmds []Cmd
	hook func(ctx context.Context, c Cmd, out io.Writer) (handled bool, err error)
}

const fakeSHA = "abcdef1234567890abcdef1234567890abcdef12"

func (s *scriptRunner) Run(ctx context.Context, c Cmd, out io.Writer) error {
	s.mu.Lock()
	s.cmds = append(s.cmds, c)
	hook := s.hook
	s.mu.Unlock()
	if hook != nil {
		if ok, err := hook(ctx, c, out); ok {
			return err
		}
	}
	switch {
	case isCmd(c, "git", "clone"):
		return os.MkdirAll(filepath.Join(c.Args[len(c.Args)-1], ".git"), 0o700)
	case isCmd(c, "git", "-C") && slices.Contains(c.Args, "rev-parse"):
		_, _ = io.WriteString(out, fakeSHA+"\n")
	case isCmd(c, "git", "-C") && slices.Contains(c.Args, "log"):
		_, _ = io.WriteString(out, "mensagem do commit\n")
	case isCmd(c, "docker", "build"):
		_, _ = io.WriteString(out, "#1 construindo\n#2 pronto\n")
	}
	return nil
}

func (s *scriptRunner) list() []Cmd {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.cmds)
}

func isCmd(c Cmd, name, first string) bool {
	return c.Name == name && len(c.Args) > 0 && c.Args[0] == first
}

type fakeTokens struct{ token string }

func (f fakeTokens) CloneToken(context.Context, string) (string, error) { return f.token, nil }

type harness struct {
	t      *testing.T
	ctx    context.Context
	db     *store.DB
	box    *secrets.Box
	hub    *events.Hub
	rec    *recorder
	runner *scriptRunner
	dock   *dockertest.Fake
	router *fakeRouter
	svc    *Service
	app    store.App

	mu      sync.Mutex
	health  func(id string) docker.Details
	runErr  func(n int, s docker.RunSpec) error
	runN    int
	labeled map[string][]docker.Container // valor do label korego.deploy → containers
	specs   []docker.RunSpec

	statusMu sync.Mutex
	statuses []StatusEvent
}

var testNow = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

func newHarness(t *testing.T, mut func(a *store.App)) *harness {
	t.Helper()
	fastPoll(t)
	ctx := context.Background()
	db := store.OpenMemory(t)
	box, err := secrets.NewBox(make([]byte, 32))
	require.NoError(t, err)
	h := &harness{
		t: t, ctx: ctx, db: db, box: box, hub: events.NewHub(), rec: &recorder{},
		runner:  &scriptRunner{},
		labeled: map[string][]docker.Container{},
		health:  func(string) docker.Details { return det("running", "healthy") },
	}
	h.router = &fakeRouter{db: db, rec: h.rec}
	h.dock = &dockertest.Fake{
		RunFn: func(_ context.Context, s docker.RunSpec) (string, error) {
			h.mu.Lock()
			n := h.runN
			h.runN++
			h.specs = append(h.specs, s)
			fn := h.runErr
			h.mu.Unlock()
			h.rec.add("run:%s:%d", s.Name, s.Ports[0].HostPort)
			if fn != nil {
				if err := fn(n, s); err != nil {
					return "", err
				}
			}
			return "cid-" + s.Labels["korego.deploy"], nil
		},
		InspectFn: func(_ context.Context, id string) (docker.Details, error) {
			h.rec.add("inspect:%s", id)
			h.mu.Lock()
			fn := h.health
			h.mu.Unlock()
			return fn(id), nil
		},
		RemoveForceFn: func(_ context.Context, id string) error {
			h.rec.add("remove:%s", id)
			if a, err := h.db.GetApp(context.Background(), h.app.ID); err == nil && a.CurrentDeployID != nil {
				h.rec.add("current-at-remove:%s=%d", id, *a.CurrentDeployID)
			}
			return nil
		},
		ImagesFn:      func(context.Context, string) ([]docker.Image, error) { return nil, nil },
		RemoveImageFn: func(context.Context, string) error { return nil },
		PullFn:        func(context.Context, string, func(string)) error { return nil },
		ListByLabelFn: func(_ context.Context, key, value string) ([]docker.Container, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.rec.add("list:%s=%s", key, value)
			return h.labeled[key+"="+value], nil
		},
	}

	p := &store.Project{Name: "site"}
	require.NoError(t, db.InsertProject(ctx, p))
	app := store.App{
		ProjectID: p.ID, Slug: "blog", SourceType: "git", Repo: "https://exemplo.com/blog.git",
		BuildType: "dockerfile", Branch: "main", DockerfilePath: "Dockerfile", InternalPort: 8080,
	}
	if mut != nil {
		mut(&app)
	}
	require.NoError(t, db.InsertApp(ctx, &app))
	h.app = app

	dataDir := t.TempDir() // antes da fila: a limpeza do diretório roda depois do Close
	q := jobs.NewQueue(2)
	t.Cleanup(q.Close)
	h.svc = &Service{
		DB: db, Docker: h.dock, Runner: h.runner, Box: box, Hub: h.hub, Queue: q, Router: h.router,
		DataDir: dataDir,
		Now:     func() time.Time { return testNow },
		Dial:    func(string) error { return errors.New("sem conexão") },
	}
	h.svc.listen = func(string) error { return nil }

	ch, cancel := h.hub.Subscribe("deploys")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range ch {
			h.statusMu.Lock()
			h.statuses = append(h.statuses, ev.Data.(StatusEvent))
			h.statusMu.Unlock()
		}
	}()
	t.Cleanup(func() { q.Close(); cancel(); <-done })
	return h
}

func (h *harness) setHealth(fn func(id string) docker.Details) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.health = fn
}

// seedCurrent grava um deploy running como o atual da app, na porta dada.
func (h *harness) seedCurrent(port int, cid string) store.Deploy {
	h.t.Helper()
	d := store.Deploy{AppID: h.app.ID, Trigger: "manual", Status: "running", Image: "korego/blog:0000000",
		HostPort: port, ContainerID: cid, CommitSHA: "0000000aaaa"}
	require.NoError(h.t, h.db.InsertDeploy(h.ctx, &d))
	require.NoError(h.t, h.db.SetCurrentDeploy(h.ctx, h.app.ID, d.ID))
	a, err := h.db.GetApp(h.ctx, h.app.ID)
	require.NoError(h.t, err)
	a.HostPort = port
	require.NoError(h.t, h.db.UpdateApp(h.ctx, &a))
	h.app = a
	return d
}

var terminal = []string{"running", "failed", "superseded", "cancelled"}

func (h *harness) wait(id int64, statuses ...string) store.Deploy {
	h.t.Helper()
	if len(statuses) == 0 {
		statuses = terminal
	}
	var d store.Deploy
	require.Eventually(h.t, func() bool {
		var err error
		d, err = h.db.GetDeploy(h.ctx, id)
		require.NoError(h.t, err)
		return slices.Contains(statuses, d.Status) && LogFinished(h.svc.DataDir, id)
	}, 10*time.Second, 5*time.Millisecond, "deploy %d não chegou em %v", id, statuses)
	return d
}

func (h *harness) getApp() store.App {
	h.t.Helper()
	a, err := h.db.GetApp(h.ctx, h.app.ID)
	require.NoError(h.t, err)
	return a
}

func (h *harness) statusesOf(id int64) []string {
	h.statusMu.Lock()
	defer h.statusMu.Unlock()
	var out []string
	for _, s := range h.statuses {
		if s.ID == id {
			out = append(out, s.Status)
		}
	}
	return out
}

func TestDeploy_DockerfileHappyPath(t *testing.T) {
	h := newHarness(t, nil)
	old := h.seedCurrent(20000, "old-cid")

	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	assert.Equal(t, "queued", d.Status)
	assert.Equal(t, "manual", d.Trigger)

	got := h.wait(d.ID)
	require.Equal(t, "running", got.Status, got.Reason)
	assert.Equal(t, "korego/blog:abcdef1", got.Image)
	assert.Equal(t, 20001, got.HostPort, "20000 está em uso pelo deploy atual")
	assert.Equal(t, "cid-"+fmt.Sprint(d.ID), got.ContainerID)
	assert.Equal(t, fakeSHA, got.CommitSHA)
	assert.Equal(t, "mensagem do commit", got.CommitMessage)
	require.NotNil(t, got.StartedAt)
	assert.Equal(t, testNow.Unix(), got.StartedAt.Unix())

	a := h.getApp()
	assert.Equal(t, 20001, a.HostPort)
	require.NotNil(t, a.CurrentDeployID)
	assert.Equal(t, d.ID, *a.CurrentDeployID)

	prev := h.wait(old.ID, "superseded")
	assert.Equal(t, "superseded", prev.Status)

	// ordem: saudável → porta nova na app → Reapply → remove o antigo
	newCID := got.ContainerID
	healthy := h.rec.last("inspect:" + newCID)
	reapply := h.rec.first("reapply:")
	removeOld := h.rec.first("remove:old-cid")
	require.GreaterOrEqual(t, healthy, 0)
	assert.Equal(t, "reapply:20001", h.rec.list()[reapply], "host_port atualizado antes do Reapply")
	assert.Greater(t, reapply, healthy)
	assert.Greater(t, removeOld, reapply)
	assert.Contains(t, h.rec.list(), fmt.Sprintf("current-at-remove:old-cid=%d", d.ID),
		"SetCurrentDeploy antes de remover o antigo")
	assert.Equal(t, -1, h.rec.first("remove:"+newCID))

	// comandos: clone https, sem file://, build com a tag certa
	cmds := h.runner.list()
	require.NotEmpty(t, cmds)
	assert.Equal(t, []string{"clone", "--depth", "1", "--branch", "main", "--single-branch",
		"https://exemplo.com/blog.git", filepath.Join(h.svc.DataDir, "builds", fmt.Sprint(d.ID))}, cmds[0].Args)
	assert.True(t, slices.ContainsFunc(cmds, func(c Cmd) bool {
		return isCmd(c, "docker", "build") && slices.Contains(c.Args, "korego/blog:abcdef1")
	}))

	assert.Eventually(t, func() bool {
		return slices.Equal([]string{"queued", "building", "starting", "running"}, h.statusesOf(d.ID))
	}, time.Second, 5*time.Millisecond, "%v", h.statusesOf(d.ID))
	assert.Eventually(t, func() bool { return slices.Contains(h.statusesOf(old.ID), "superseded") },
		time.Second, 5*time.Millisecond)

	_, err = os.Stat(filepath.Join(h.svc.DataDir, "builds", fmt.Sprint(d.ID)))
	assert.True(t, os.IsNotExist(err), "builds/<id> removido")

	log, err := ReadLog(h.svc.DataDir, d.ID, 0)
	require.NoError(t, err)
	var texts []string
	for _, e := range log {
		texts = append(texts, e.Text)
	}
	for _, hdr := range []string{"==> Buscando código", "==> Construindo", "==> Subindo container",
		"==> Verificando saúde", "==> Trocando a rota", "==> Limpando"} {
		assert.Contains(t, texts, hdr)
	}
	assert.Contains(t, texts, "#2 pronto")
}

func TestDeploy_HealthFailKeepsOld(t *testing.T) {
	h := newHarness(t, func(a *store.App) { a.HealthTimeoutS = 1 })
	old := h.seedCurrent(20000, "old-cid")
	h.setHealth(func(id string) docker.Details {
		if id == "old-cid" {
			return det("running", "healthy")
		}
		return det("running", "starting")
	})

	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)
	assert.Equal(t, "failed", got.Status)
	assert.Equal(t, "verificação de saúde falhou: tempo esgotado (1s)", got.Reason)

	assert.Equal(t, -1, h.rec.first("reapply:"), "rota não muda")
	assert.Equal(t, 1, h.rec.count("remove:cid-"+fmt.Sprint(d.ID)), "novo removido")
	assert.Equal(t, -1, h.rec.first("remove:old-cid"), "antigo intacto")

	a := h.getApp()
	assert.Equal(t, 20000, a.HostPort)
	require.NotNil(t, a.CurrentDeployID)
	assert.Equal(t, old.ID, *a.CurrentDeployID)
	prev, err := h.db.GetDeploy(h.ctx, old.ID)
	require.NoError(t, err)
	assert.Equal(t, "running", prev.Status)

	// unhealthy também falha sem tocar no antigo
	h.setHealth(func(string) docker.Details { return det("running", "unhealthy") })
	d2, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got2 := h.wait(d2.ID)
	assert.Equal(t, "failed", got2.Status)
	assert.True(t, strings.HasPrefix(got2.Reason, "verificação de saúde falhou"), got2.Reason)
	assert.Equal(t, -1, h.rec.first("reapply:"))
	assert.Equal(t, -1, h.rec.first("remove:old-cid"))
}

func TestDeploy_ExitedContainer(t *testing.T) {
	h := newHarness(t, nil)
	h.setHealth(func(string) docker.Details {
		dd := det("exited", "")
		dd.ExitCode = 3
		return dd
	})
	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)
	assert.Equal(t, "failed", got.Status)
	assert.Equal(t, "o container saiu com código 3", got.Reason)
	assert.Equal(t, 1, h.rec.count("remove:cid-"+fmt.Sprint(d.ID)))
	assert.Equal(t, 0, h.getApp().HostPort)
	assert.Nil(t, h.getApp().CurrentDeployID)
}

func TestDeploy_ImageNotFoundAndRailpack(t *testing.T) {
	h := newHarness(t, nil)
	h.mu.Lock()
	h.runErr = func(int, docker.RunSpec) error { return fmt.Errorf("%w: No such image", docker.ErrNotFound) }
	h.mu.Unlock()
	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)
	assert.Equal(t, "failed", got.Status)
	assert.Equal(t, "imagem não encontrada: korego/blog:abcdef1", got.Reason)

	old := LookPath
	LookPath = func(string) (string, error) { return "", errors.New("não achou") }
	t.Cleanup(func() { LookPath = old })
	a := h.getApp()
	a.BuildType = "railpack"
	require.NoError(t, h.db.UpdateApp(h.ctx, &a))
	d2, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got2 := h.wait(d2.ID)
	assert.Equal(t, "failed", got2.Status)
	assert.Equal(t, "railpack não está instalado", got2.Reason)
}

func TestDeploy_PortConflictRetry(t *testing.T) {
	h := newHarness(t, nil)
	h.mu.Lock()
	h.runErr = func(n int, s docker.RunSpec) error {
		switch n {
		case 0:
			return errors.New("driver failed programming external connectivity: Bind for 127.0.0.1:20000 failed: port is already allocated")
		case 2:
			return fmt.Errorf("%w: name already in use", docker.ErrConflict)
		}
		return nil
	}
	h.mu.Unlock()

	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)
	require.Equal(t, "running", got.Status, got.Reason)
	assert.Equal(t, 20001, got.HostPort, "tenta de novo com outra porta")

	// conflito de nome: remove o container velho com o mesmo nome e tenta de novo
	d2, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got2 := h.wait(d2.ID)
	require.Equal(t, "running", got2.Status, got2.Reason)
	name := fmt.Sprintf("korego-blog-abcdef1-d%d", d2.ID)
	rm := h.rec.first("remove:" + name)
	require.GreaterOrEqual(t, rm, 0, "%v", h.rec.list())
	assert.Equal(t, 2, h.rec.count("run:"+name))
}

func TestDeploy_ReapplyFailRestores(t *testing.T) {
	h := newHarness(t, nil)
	old := h.seedCurrent(20000, "old-cid")
	h.router.errFn = func(n int) error {
		if n == 0 {
			return errors.New("caddy fora")
		}
		return nil
	}
	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)
	assert.Equal(t, "failed", got.Status)
	assert.Equal(t, "falha ao atualizar o proxy: caddy fora", got.Reason)

	assert.Equal(t, []string{"reapply:20001", "reapply:20000"}, filter(h.rec.list(), "reapply:"))
	assert.Equal(t, 20000, h.getApp().HostPort)
	assert.Equal(t, old.ID, *h.getApp().CurrentDeployID)
	assert.Equal(t, 1, h.rec.count("remove:cid-"+fmt.Sprint(d.ID)))
	assert.Equal(t, -1, h.rec.first("remove:old-cid"))
	prev, _ := h.db.GetDeploy(h.ctx, old.ID)
	assert.Equal(t, "running", prev.Status)
}

func filter(l []string, prefix string) []string {
	var out []string
	for _, e := range l {
		if strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return out
}

func TestDeploy_SecretsRedactedInLog(t *testing.T) {
	const token = "ghs_tokentokentoken123"
	const value = "valor-super-secreto"
	h := newHarness(t, func(a *store.App) { a.SourceType = "github"; a.Repo = "dono/blog" })
	h.svc.Tokens = fakeTokens{token: token}
	enc, err := h.box.Encrypt([]byte(value))
	require.NoError(t, err)
	require.NoError(t, h.db.SetAppEnv(h.ctx, h.app.ID, map[string]string{"API_KEY": enc}, nil))
	cred := TokenSecrets(token)[1]

	gate := make(chan struct{})
	h.runner.hook = func(_ context.Context, c Cmd, out io.Writer) (bool, error) {
		if isCmd(c, "git", "clone") {
			<-gate
			_, _ = io.WriteString(out, "Cloning https://x-access-token:"+token+"@github.com/dono/blog.git\n")
		}
		if isCmd(c, "docker", "build") {
			_, _ = io.WriteString(out, "echo "+value+" e "+token+" e "+cred+"\n")
			_, _ = io.WriteString(out, "sem fim de linha "+value)
			return true, nil
		}
		return false, nil
	}

	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	ch, cancel := h.hub.Subscribe(fmt.Sprintf("deploy-logs:%d", d.ID))
	defer cancel()
	close(gate)
	got := h.wait(d.ID)
	require.Equal(t, "running", got.Status, got.Reason)

	raw, err := os.ReadFile(filepath.Join(h.svc.DataDir, "logs", fmt.Sprintf("deploy-%d.log", d.ID)))
	require.NoError(t, err)
	for _, s := range []string{token, value, cred} {
		assert.NotContains(t, string(raw), s)
	}
	assert.Contains(t, string(raw), "echo •••• e •••• e ••••")
	assert.Contains(t, string(raw), "sem fim de linha ••••")

	var topic strings.Builder
	for loop := true; loop; {
		select {
		case ev := <-ch:
			if e, ok := ev.Data.(LogEntry); ok {
				topic.WriteString(e.Text + "\n")
			}
		case <-time.After(200 * time.Millisecond):
			loop = false
		}
	}
	assert.Contains(t, topic.String(), "echo •••• e •••• e ••••")
	for _, s := range []string{token, value, cred} {
		assert.NotContains(t, topic.String(), s)
	}

	// token só no ambiente do git clone; nenhum segredo em argumentos
	cmds := h.runner.list()
	assert.Equal(t, "https://github.com/dono/blog.git", cmds[0].Args[6])
	assert.True(t, slices.ContainsFunc(cmds[0].Env, func(e string) bool { return strings.Contains(e, cred) }))
	for _, c := range cmds {
		for _, a := range c.Args {
			assert.NotContains(t, a, token)
			assert.NotContains(t, a, value)
		}
	}
	h.mu.Lock()
	assert.Equal(t, []string{"API_KEY=" + value}, h.specs[0].Env)
	h.mu.Unlock()
}

func TestDeploy_GitHubWithoutTokens(t *testing.T) {
	h := newHarness(t, func(a *store.App) { a.SourceType = "github"; a.Repo = "dono/blog" })
	_, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	assert.ErrorIs(t, err, ErrNoGitHub)
	list, err := h.db.ListDeploys(h.ctx, h.app.ID, 10)
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestDeploy_SupersededInQueue(t *testing.T) {
	h := newHarness(t, nil)
	gate := make(chan struct{})
	started := make(chan struct{}, 4)
	h.runner.hook = func(_ context.Context, c Cmd, _ io.Writer) (bool, error) {
		if isCmd(c, "git", "clone") {
			started <- struct{}{}
			<-gate
		}
		return false, nil
	}
	a, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	<-started // A rodando
	b, err := h.svc.Deploy(h.ctx, h.app.ID, "webhook", "1111111", "b")
	require.NoError(t, err)
	assert.Equal(t, "1111111", b.CommitSHA)
	c, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)

	gotB := h.wait(b.ID)
	assert.Equal(t, "superseded", gotB.Status)
	assert.Eventually(t, func() bool { return slices.Contains(h.statusesOf(b.ID), "superseded") },
		time.Second, 5*time.Millisecond)

	close(gate)
	assert.Equal(t, "superseded", h.wait(a.ID, "superseded").Status, "A foi substituído por C ao fim")
	assert.Equal(t, "running", h.wait(c.ID).Status)
}

func TestDeploy_CancelRunning(t *testing.T) {
	h := newHarness(t, nil)
	building := make(chan struct{}, 1)
	h.runner.hook = func(ctx context.Context, c Cmd, _ io.Writer) (bool, error) {
		if isCmd(c, "docker", "build") {
			building <- struct{}{}
			<-ctx.Done()
			return true, ctx.Err()
		}
		return false, nil
	}
	a, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	<-building
	b, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)

	// em espera: sai da fila e vira cancelled na hora
	require.NoError(t, h.svc.Cancel(h.ctx, b.ID))
	gotB, err := h.db.GetDeploy(h.ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, "cancelled", gotB.Status)

	// rodando: ctx cancelado → cancelled, com limpeza
	require.NoError(t, h.svc.Cancel(h.ctx, a.ID))
	gotA := h.wait(a.ID)
	assert.Equal(t, "cancelled", gotA.Status)
	assert.Equal(t, -1, h.rec.first("run:"), "nenhum container criado")
	_, err = os.Stat(filepath.Join(h.svc.DataDir, "builds", fmt.Sprint(a.ID)))
	assert.True(t, os.IsNotExist(err))

	assert.ErrorIs(t, h.svc.Cancel(h.ctx, a.ID), ErrDeployFinished)
	assert.ErrorIs(t, h.svc.Cancel(h.ctx, 9999), store.ErrNotFound)
}

func TestRollback_UnsupportedAndImageGone(t *testing.T) {
	h := newHarness(t, nil)
	var ids []int64
	for i := range 4 {
		d := store.Deploy{AppID: h.app.ID, Trigger: "manual", Status: "superseded", Image: fmt.Sprintf("korego/blog:000000%d", i)}
		require.NoError(t, h.db.InsertDeploy(h.ctx, &d))
		ids = append(ids, d.ID)
	}
	_, err := h.svc.Rollback(h.ctx, h.app.ID, ids[0])
	assert.ErrorIs(t, err, ErrImageGone, "fora das 3 mais recentes")

	failed := store.Deploy{AppID: h.app.ID, Trigger: "manual", Status: "failed", Image: "korego/blog:fffffff"}
	require.NoError(t, h.db.InsertDeploy(h.ctx, &failed))
	_, err = h.svc.Rollback(h.ctx, h.app.ID, failed.ID)
	assert.ErrorIs(t, err, ErrImageGone)

	_, err = h.svc.Rollback(h.ctx, h.app.ID+100, ids[3])
	assert.ErrorIs(t, err, store.ErrNotFound)

	comp := store.App{ProjectID: h.app.ProjectID, Slug: "stack", SourceType: "git", Repo: "https://exemplo.com/s.git",
		BuildType: "compose", Branch: "main", ComposePath: "docker-compose.yml", ComposeService: "web", InternalPort: 80}
	require.NoError(t, h.db.InsertApp(h.ctx, &comp))
	_, err = h.svc.Rollback(h.ctx, comp.ID, ids[3])
	assert.ErrorIs(t, err, ErrRollbackUnsupported)

	// deploy de outra app
	other := store.App{ProjectID: h.app.ProjectID, Slug: "outra", SourceType: "image", Image: "nginx:1",
		BuildType: "image", InternalPort: 80}
	require.NoError(t, h.db.InsertApp(h.ctx, &other))
	_, err = h.svc.Rollback(h.ctx, other.ID, ids[3])
	assert.ErrorIs(t, err, store.ErrNotFound)
}

func TestRollback_SkipsBuild(t *testing.T) {
	h := newHarness(t, nil)
	src := store.Deploy{AppID: h.app.ID, Trigger: "manual", Status: "superseded", Image: "korego/blog:aaaaaaa",
		CommitSHA: "aaaaaaa111", CommitMessage: "antigo"}
	require.NoError(t, h.db.InsertDeploy(h.ctx, &src))
	cur := h.seedCurrent(20000, "cur-cid")

	d, err := h.svc.Rollback(h.ctx, h.app.ID, src.ID)
	require.NoError(t, err)
	assert.Equal(t, "rollback", d.Trigger)
	require.NotNil(t, d.RollbackOf)
	assert.Equal(t, src.ID, *d.RollbackOf)

	got := h.wait(d.ID)
	require.Equal(t, "running", got.Status, got.Reason)
	assert.Equal(t, "korego/blog:aaaaaaa", got.Image)
	assert.Equal(t, "aaaaaaa111", got.CommitSHA)
	assert.Equal(t, "antigo", got.CommitMessage)
	assert.Empty(t, h.runner.list(), "sem git nem docker build")
	h.mu.Lock()
	assert.Equal(t, "korego/blog:aaaaaaa", h.specs[0].Image)
	h.mu.Unlock()
	prev := h.wait(cur.ID, "superseded")
	assert.Equal(t, "superseded", prev.Status)
	assert.Greater(t, h.rec.first("remove:cur-cid"), h.rec.first("reapply:"))
}

func TestRecover_InterruptedAndOrphans(t *testing.T) {
	h := newHarness(t, nil)
	cur := h.seedCurrent(20000, "c-cur")

	interrupted := store.Deploy{AppID: h.app.ID, Trigger: "manual", Status: "building"}
	require.NoError(t, h.db.InsertDeploy(h.ctx, &interrupted))
	require.NoError(t, os.MkdirAll(filepath.Join(h.svc.DataDir, "builds", fmt.Sprint(interrupted.ID)), 0o700))

	// outra app cujo deploy atual (artificialmente) estava starting: não é removido
	app2 := store.App{ProjectID: h.app.ProjectID, Slug: "api", SourceType: "git", Repo: "https://exemplo.com/api.git",
		BuildType: "dockerfile", Branch: "main", DockerfilePath: "Dockerfile", InternalPort: 8080}
	require.NoError(t, h.db.InsertApp(h.ctx, &app2))
	starting := store.Deploy{AppID: app2.ID, Trigger: "manual", Status: "starting"}
	require.NoError(t, h.db.InsertDeploy(h.ctx, &starting))
	require.NoError(t, h.db.SetCurrentDeploy(h.ctx, app2.ID, starting.ID))

	queued := store.Deploy{AppID: h.app.ID, Trigger: "manual", Status: "queued"}
	require.NoError(t, h.db.InsertDeploy(h.ctx, &queued))

	h.mu.Lock()
	h.labeled["korego.deploy="+fmt.Sprint(interrupted.ID)] = []docker.Container{{ID: "c-orfao"}}
	h.labeled["korego.deploy="+fmt.Sprint(starting.ID)] = []docker.Container{{ID: "c-starting"}}
	h.mu.Unlock()

	// segura o deploy reenfileirado até conferir o que o Recover fez
	gate := make(chan struct{})
	h.runner.hook = func(_ context.Context, c Cmd, _ io.Writer) (bool, error) {
		if isCmd(c, "git", "clone") {
			<-gate
		}
		return false, nil
	}
	require.NoError(t, h.svc.Recover(h.ctx))

	gi, _ := h.db.GetDeploy(h.ctx, interrupted.ID)
	assert.Equal(t, "failed", gi.Status)
	assert.Equal(t, "interrompido", gi.Reason)
	gs, _ := h.db.GetDeploy(h.ctx, starting.ID)
	assert.Equal(t, "running", gs.Status, "o atual interrompido volta a running")

	assert.GreaterOrEqual(t, h.rec.first("remove:c-orfao"), 0)
	assert.Equal(t, -1, h.rec.first("remove:c-starting"), "deploy atual preservado")
	assert.Equal(t, -1, h.rec.first("remove:c-cur"))
	_, err := os.Stat(filepath.Join(h.svc.DataDir, "builds", fmt.Sprint(interrupted.ID)))
	assert.True(t, os.IsNotExist(err))

	assert.Eventually(t, func() bool {
		return slices.Contains(h.statusesOf(interrupted.ID), "failed")
	}, time.Second, 5*time.Millisecond)

	close(gate)
	gq := h.wait(queued.ID)
	assert.Equal(t, "running", gq.Status, gq.Reason)
	prev := h.wait(cur.ID, "superseded")
	assert.Equal(t, "superseded", prev.Status)
}

func asCompose(a *store.App) {
	a.BuildType = "compose"
	a.ComposePath = "docker-compose.yml"
	a.ComposeService = "web"
	a.InternalPort = 80
}

func TestDeploy_ComposeKeepsPort(t *testing.T) {
	h := newHarness(t, asCompose)
	const value = "valor-do-compose"
	enc, err := h.box.Encrypt([]byte(value))
	require.NoError(t, err)
	require.NoError(t, h.db.SetAppEnv(h.ctx, h.app.ID, map[string]string{"DB_PASS": enc}, nil))
	h.mu.Lock()
	h.labeled["com.docker.compose.project=korego-blog"] = []docker.Container{
		{ID: "c-db", Service: "db"}, {ID: "c-web", Service: "web"},
	}
	h.mu.Unlock()

	d1, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	g1 := h.wait(d1.ID)
	require.Equal(t, "running", g1.Status, g1.Reason)
	assert.Equal(t, 20000, g1.HostPort)
	assert.Equal(t, "c-web", g1.ContainerID)
	assert.Equal(t, 20000, h.getApp().HostPort)
	assert.GreaterOrEqual(t, h.rec.first("inspect:c-web"), 0)

	appDir := AppDir(h.svc.DataDir, "blog")
	override := filepath.Join(appDir, "docker-compose.korego.yml")
	cmds := h.runner.list()
	assert.Equal(t, appDir, cmds[0].Args[len(cmds[0].Args)-1], "clone no diretório persistente")
	var up, build *Cmd
	for i := range cmds {
		c := &cmds[i]
		if isCmd(*c, "docker", "compose") && slices.Contains(c.Args, "up") {
			up = c
		}
		if isCmd(*c, "docker", "compose") && slices.Contains(c.Args, "build") {
			build = c
		}
	}
	require.NotNil(t, up)
	require.NotNil(t, build)
	assert.Contains(t, up.Args, override)
	assert.Contains(t, up.Args, filepath.Join(appDir, "docker-compose.yml"))
	for _, c := range []*Cmd{up, build} {
		assert.Contains(t, c.Env, "DB_PASS="+value, "variáveis para a interpolação ${VAR}")
		for _, a := range c.Args {
			assert.NotContains(t, a, value)
		}
	}
	st, err := os.Stat(appDir)
	require.NoError(t, err, "diretório da app sobrevive ao deploy")
	assert.True(t, st.IsDir())
	_, err = os.Stat(override)
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(h.svc.DataDir, "builds", fmt.Sprint(d1.ID)))
	assert.True(t, os.IsNotExist(err), "builds/<id> não é usado no Compose")
	raw, err := os.ReadFile(override)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), value, "variáveis nunca em disco")

	d2, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	g2 := h.wait(d2.ID)
	require.Equal(t, "running", g2.Status, g2.Reason)
	assert.Equal(t, 20000, g2.HostPort, "porta fixa da app")
	// 2º deploy: fetch + reset no diretório existente, sem clone nem clean
	cmds = h.runner.list()
	var second []Cmd
	for _, c := range cmds {
		if c.Name == "git" {
			second = append(second, c)
		}
	}
	require.GreaterOrEqual(t, len(second), 8)
	assert.Equal(t, []string{"-C", appDir, "fetch", "--depth", "1", "origin", "main"}, second[4].Args)
	assert.Equal(t, []string{"-C", appDir, "reset", "--hard", "FETCH_HEAD"}, second[5].Args)
	for _, c := range cmds {
		assert.NotContains(t, c.Args, "clean")
	}
	p1 := h.wait(d1.ID, "superseded")
	assert.Equal(t, "superseded", p1.Status)
	assert.Equal(t, -1, h.rec.first("remove:"), "Compose não remove containers")
	assert.Equal(t, -1, h.rec.first("run:"))
}
