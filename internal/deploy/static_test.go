package deploy

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engviniciusbs/korego/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// staticHarness monta uma app estática (git) com wwwRoot num diretório
// temporário. files é o conteúdo que o "git clone" falso deixa no clone.
func staticHarness(t *testing.T, files map[string]string, mut func(a *store.App)) (*harness, string) {
	t.Helper()
	www := t.TempDir()
	old := wwwRoot
	wwwRoot = www
	t.Cleanup(func() { wwwRoot = old })
	h := newHarness(t, func(a *store.App) {
		a.BuildType, a.DockerfilePath, a.InternalPort = "static", "", 0
		a.StaticSPA = true
		if mut != nil {
			mut(a)
		}
	})
	h.runner.hook = func(_ context.Context, c Cmd, _ io.Writer) (bool, error) {
		if isCmd(c, "git", "clone") {
			dir := c.Args[len(c.Args)-1]
			require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o700))
			for n, body := range files {
				p := filepath.Join(dir, filepath.FromSlash(n))
				require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
				require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
			}
			return true, nil
		}
		return false, nil
	}
	return h, filepath.Join(www, "blog")
}

// buildWrites faz o "docker run" falso criar dist/index.html dentro de <src>.
func buildWrites(t *testing.T, h *harness, next func(ctx context.Context) error) {
	prev := h.runner.hook
	h.runner.hook = func(ctx context.Context, c Cmd, out io.Writer) (bool, error) {
		if isCmd(c, "docker", "run") {
			src := mountSrc(c)
			require.NoError(t, os.MkdirAll(filepath.Join(src, "dist"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(src, "dist", "index.html"), []byte("<html>"), 0o644))
			if next != nil {
				return true, next(ctx)
			}
			return true, nil
		}
		return prev(ctx, c, out)
	}
}

func mountSrc(c Cmd) string {
	i := slices.Index(c.Args, "-v")
	return strings.TrimSuffix(c.Args[i+1], ":/app")
}

func dockerRuns(h *harness) []Cmd {
	var out []Cmd
	for _, c := range h.runner.list() {
		if isCmd(c, "docker", "run") {
			out = append(out, c)
		}
	}
	return out
}

func releasesIn(t *testing.T, root string) []string {
	t.Helper()
	es, err := os.ReadDir(filepath.Join(root, "releases"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

// seedRelease cria uma versão com index.html e um deploy running que a usa.
func seedRelease(t *testing.T, h *harness, root, name string, current bool) store.Deploy {
	t.Helper()
	dir := filepath.Join(root, "releases", name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte(name), 0o644))
	d := store.Deploy{AppID: h.app.ID, Trigger: "manual", Status: "superseded", CommitSHA: name[15:]}
	if current {
		d.Status = "running"
	}
	require.NoError(t, h.db.InsertDeploy(h.ctx, &d))
	require.NoError(t, h.db.SetDeployRelease(h.ctx, d.ID, dir))
	if current {
		require.NoError(t, SwapCurrent(root, dir))
		require.NoError(t, h.db.SetCurrentDeploy(h.ctx, h.app.ID, d.ID))
	}
	d.ReleaseDir = dir
	return d
}

func currentOf(t *testing.T, root string) string {
	t.Helper()
	cur, err := CurrentRelease(root)
	require.NoError(t, err)
	return cur
}

func TestStatic_DeployPublishesAndSwaps(t *testing.T) {
	linuxOnly(t)
	h, root := staticHarness(t, map[string]string{"package.json": "{}", "package-lock.json": "{}"}, nil)
	buildWrites(t, h, nil)
	require.NoError(t, h.db.SetAppEnv(h.ctx, h.app.ID, map[string]string{"VITE_API": mustEnc(t, h, "segredo-da-api")}, nil))
	old := seedRelease(t, h, root, "20261101000000-1111111", true)

	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)

	require.Equal(t, "running", got.Status, got.Reason)
	want := filepath.Join(root, "releases", ReleaseName(testNow, fakeSHA))
	assert.Equal(t, want, got.ReleaseDir)
	assert.Equal(t, want, currentOf(t, root))
	assert.FileExists(t, filepath.Join(want, "index.html"))
	assert.Equal(t, 0, got.HostPort)
	assert.Equal(t, d.ID, *h.getApp().CurrentDeployID)
	prev, err := h.db.GetDeploy(h.ctx, old.ID)
	require.NoError(t, err)
	assert.Equal(t, "superseded", prev.Status)
	assert.DirExists(t, old.ReleaseDir, "a versão anterior fica para rollback")

	runs := dockerRuns(h)
	require.Len(t, runs, 1)
	a := runs[0].Args
	assert.Equal(t, []string{"run", "--rm"}, a[:2])
	assert.Contains(t, strings.Join(a, " "), "--cpus 2 --memory 2g")
	assert.Contains(t, strings.Join(a, " "), "-w /app -e")
	assert.Contains(t, strings.Join(a, " "), "-e VITE_API --name "+buildContainerName(d.ID)+" node:22-alpine sh -c npm ci && npm run build")
	assert.Contains(t, runs[0].Env, "VITE_API=segredo-da-api")
	for _, arg := range a {
		assert.NotContains(t, arg, "segredo-da-api", "valor da variável não vai nos argumentos")
	}
	assert.Equal(t, 0, h.runN, "nenhum container criado")
	assert.Equal(t, 0, h.rec.count("reapply:"), "app estática não mexe no proxy")
	assert.NoDirExists(t, h.svc.buildDir(d.ID))
}

func mustEnc(t *testing.T, h *harness, v string) string {
	t.Helper()
	b, err := h.box.Encrypt([]byte(v))
	require.NoError(t, err)
	return b
}

func TestStatic_NoPackageJSONCopiesAsIs(t *testing.T) {
	h, root := staticHarness(t, map[string]string{"index.html": "<html>", "css/a.css": "x"},
		func(a *store.App) { a.StaticOutputDir = "." })

	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)

	require.Equal(t, "running", got.Status, got.Reason)
	assert.Empty(t, dockerRuns(h), "sem package.json não há build")
	cur := currentOf(t, root)
	assert.FileExists(t, filepath.Join(cur, "index.html"))
	assert.FileExists(t, filepath.Join(cur, "css", "a.css"))
	assert.NoDirExists(t, filepath.Join(cur, ".git"), ".git nunca é publicado")
}

func TestStatic_GitRemovalFailsDeploy(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("depende de permissões POSIX sem root")
	}
	h, root := staticHarness(t, map[string]string{"index.html": "<html>"},
		func(a *store.App) { a.StaticOutputDir = "." })
	prev := h.runner.hook
	h.runner.hook = func(ctx context.Context, c Cmd, out io.Writer) (bool, error) {
		ok, err := prev(ctx, c, out)
		if isCmd(c, "git", "clone") {
			git := filepath.Join(c.Args[len(c.Args)-1], ".git")
			require.NoError(t, os.WriteFile(filepath.Join(git, "HEAD"), []byte("x"), 0o600))
			require.NoError(t, os.Chmod(git, 0o500)) // RemoveAll não apaga o HEAD
			t.Cleanup(func() { _ = os.Chmod(git, 0o700) })
		}
		return ok, err
	}

	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)
	assert.Equal(t, "failed", got.Status)
	assert.Contains(t, got.Reason, ".git")
	assert.Empty(t, releasesIn(t, root), "nada publicado")
}

func TestStatic_SymlinkedWWWRoot(t *testing.T) {
	linuxOnly(t)
	h, _ := staticHarness(t, map[string]string{"index.html": "<html>"},
		func(a *store.App) { a.StaticOutputDir = "." })
	// /var/www é um link para a pasta real; a adoção grava a raiz resolvida.
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "www")
	require.NoError(t, os.Symlink(real, link))
	wwwRoot = link
	app := h.getApp()
	app.PublishRoot = filepath.Join(real, "blog")
	require.NoError(t, h.db.UpdateAppConfig(h.ctx, &app))
	h.app = app

	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)
	require.Equal(t, "running", got.Status, got.Reason)
	assert.FileExists(t, filepath.Join(currentOf(t, filepath.Join(real, "blog")), "index.html"))

	// Fora do wwwRoot continua recusado, também resolvendo os links.
	app.PublishRoot = filepath.Join(t.TempDir(), "blog")
	require.NoError(t, h.db.UpdateAppConfig(h.ctx, &app))
	d, err = h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got = h.wait(d.ID)
	assert.Equal(t, "failed", got.Status)
	assert.Contains(t, got.Reason, "raiz de publicação inválida")
}

func TestStatic_BuildFailsKeepsCurrent(t *testing.T) {
	linuxOnly(t)
	h, root := staticHarness(t, map[string]string{"package.json": "{}"}, nil)
	buildWrites(t, h, func(context.Context) error { return errors.New("exit status 1") })
	old := seedRelease(t, h, root, "20261101000000-1111111", true)

	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)

	assert.Equal(t, "failed", got.Status)
	assert.Contains(t, got.Reason, "falha no build")
	assert.Equal(t, old.ReleaseDir, currentOf(t, root))
	assert.Equal(t, []string{"20261101000000-1111111"}, releasesIn(t, root))
	assert.Equal(t, old.ID, *h.getApp().CurrentDeployID)
	assert.Equal(t, 1, h.rec.count("remove:"+buildContainerName(d.ID)), "container de build removido")
}

func TestStatic_PublishFailsRemovesRelease(t *testing.T) {
	linuxOnly(t)
	// saída sem index.html (SPA): PublishRelease falha e não sobra pasta.
	h, root := staticHarness(t, map[string]string{"dist/a.txt": "x"}, nil)
	old := seedRelease(t, h, root, "20261101000000-1111111", true)

	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)

	assert.Equal(t, "failed", got.Status)
	assert.Equal(t, old.ReleaseDir, currentOf(t, root))
	assert.Equal(t, []string{"20261101000000-1111111"}, releasesIn(t, root))
}

func TestStatic_OutputOutsideCloneFails(t *testing.T) {
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "index.html"), []byte("x"), 0o644))
	h, root := staticHarness(t, nil, nil)
	prev := h.runner.hook
	h.runner.hook = func(ctx context.Context, c Cmd, out io.Writer) (bool, error) {
		ok, err := prev(ctx, c, out)
		if isCmd(c, "git", "clone") {
			require.NoError(t, os.Symlink(outside, filepath.Join(c.Args[len(c.Args)-1], "dist")))
		}
		return ok, err
	}

	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)

	assert.Equal(t, "failed", got.Status)
	assert.Contains(t, got.Reason, "pasta de saída inválida")
	assert.Empty(t, releasesIn(t, root))
}

func TestStatic_CancelDuringBuild(t *testing.T) {
	linuxOnly(t)
	h, root := staticHarness(t, map[string]string{"package.json": "{}"}, nil)
	started := make(chan struct{})
	buildWrites(t, h, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	old := seedRelease(t, h, root, "20261101000000-1111111", true)

	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("build não começou")
	}
	require.NoError(t, h.svc.Cancel(h.ctx, d.ID))
	got := h.wait(d.ID)

	assert.Equal(t, "cancelled", got.Status)
	assert.Equal(t, old.ReleaseDir, currentOf(t, root))
	assert.Equal(t, []string{"20261101000000-1111111"}, releasesIn(t, root))
	assert.Equal(t, old.ID, *h.getApp().CurrentDeployID)
	assert.Equal(t, 1, h.rec.count("remove:"+buildContainerName(d.ID)), "container de build removido")
}

func TestStatic_Rollback(t *testing.T) {
	linuxOnly(t)
	h, root := staticHarness(t, nil, nil)
	a := seedRelease(t, h, root, "20261101000000-aaaaaaa", false)
	b := seedRelease(t, h, root, "20261102000000-bbbbbbb", true)

	d, err := h.svc.Rollback(h.ctx, h.app.ID, a.ID)
	require.NoError(t, err)
	assert.Equal(t, a.ReleaseDir, d.ReleaseDir)
	got := h.wait(d.ID)

	require.Equal(t, "running", got.Status, got.Reason)
	assert.Equal(t, "rollback", got.Trigger)
	assert.Equal(t, a.ReleaseDir, got.ReleaseDir)
	assert.Equal(t, a.ReleaseDir, currentOf(t, root))
	assert.Equal(t, d.ID, *h.getApp().CurrentDeployID)
	assert.Empty(t, dockerRuns(h))
	assert.Empty(t, h.runner.list(), "rollback não busca código nem constrói")
	pb, err := h.db.GetDeploy(h.ctx, b.ID)
	require.NoError(t, err)
	assert.Equal(t, "superseded", pb.Status)
	assert.DirExists(t, b.ReleaseDir)
}

func TestStatic_RollbackGone(t *testing.T) {
	h, root := staticHarness(t, nil, nil)
	a := seedRelease(t, h, root, "20261101000000-aaaaaaa", false)
	seedRelease(t, h, root, "20261102000000-bbbbbbb", true)
	require.NoError(t, os.RemoveAll(a.ReleaseDir))

	_, err := h.svc.Rollback(h.ctx, h.app.ID, a.ID)
	assert.ErrorIs(t, err, ErrReleaseGone)
}

func TestStatic_PruneKeepsFiveAndForeign(t *testing.T) {
	linuxOnly(t)
	h, root := staticHarness(t, map[string]string{"package.json": "{}"}, nil)
	buildWrites(t, h, nil)
	for i := 1; i <= 6; i++ {
		seedRelease(t, h, root, "2026110"+string(rune('0'+i))+"000000-1111111", i == 1)
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "releases", "antiga"), 0o755))

	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)
	require.Equal(t, "running", got.Status, got.Reason)

	rs := releasesIn(t, root)
	assert.Contains(t, rs, "antiga", "pasta fora do padrão sobrevive")
	assert.Contains(t, rs, "20261101000000-1111111", "versão do deploy anterior sobrevive")
	assert.Contains(t, rs, filepath.Base(got.ReleaseDir))
	assert.NotContains(t, rs, "20261102000000-1111111")
	assert.Len(t, rs, 7) // 5 mais recentes + anterior + "antiga"
}

func TestStatic_RecoverInterrupted(t *testing.T) {
	linuxOnly(t)
	t.Run("current é a versão do deploy", func(t *testing.T) {
		h, root := staticHarness(t, nil, nil)
		old := seedRelease(t, h, root, "20261101000000-1111111", true)
		newDir := filepath.Join(root, "releases", "20261102000000-2222222")
		require.NoError(t, os.MkdirAll(newDir, 0o755))
		d := store.Deploy{AppID: h.app.ID, Trigger: "manual", Status: "starting"}
		require.NoError(t, h.db.InsertDeploy(h.ctx, &d))
		require.NoError(t, h.db.SetDeployRelease(h.ctx, d.ID, newDir))
		require.NoError(t, SwapCurrent(root, newDir)) // caiu depois da troca

		require.NoError(t, h.svc.Recover(h.ctx))

		got, err := h.db.GetDeploy(h.ctx, d.ID)
		require.NoError(t, err)
		assert.Equal(t, "running", got.Status)
		assert.Equal(t, d.ID, *h.getApp().CurrentDeployID)
		po, err := h.db.GetDeploy(h.ctx, old.ID)
		require.NoError(t, err)
		assert.Equal(t, "superseded", po.Status)
		assert.DirExists(t, newDir)
		assert.Equal(t, newDir, currentOf(t, root))
	})
	t.Run("current é outra versão", func(t *testing.T) {
		h, root := staticHarness(t, nil, nil)
		old := seedRelease(t, h, root, "20261101000000-1111111", true)
		newDir := filepath.Join(root, "releases", "20261102000000-2222222")
		require.NoError(t, os.MkdirAll(newDir, 0o755))
		d := store.Deploy{AppID: h.app.ID, Trigger: "manual", Status: "building"}
		require.NoError(t, h.db.InsertDeploy(h.ctx, &d))
		require.NoError(t, h.db.SetDeployRelease(h.ctx, d.ID, newDir))

		require.NoError(t, h.svc.Recover(h.ctx))

		got, err := h.db.GetDeploy(h.ctx, d.ID)
		require.NoError(t, err)
		assert.Equal(t, "failed", got.Status)
		assert.Equal(t, old.ID, *h.getApp().CurrentDeployID)
		assert.NoDirExists(t, newDir)
		assert.Equal(t, old.ReleaseDir, currentOf(t, root))
		po, err := h.db.GetDeploy(h.ctx, old.ID)
		require.NoError(t, err)
		assert.Equal(t, "running", po.Status)
		assert.Equal(t, 1, h.rec.count("remove:"+buildContainerName(d.ID)))
	})
}

func TestStaticBuildCmd(t *testing.T) {
	src := t.TempDir()
	app := store.App{BuildType: "static"}
	_, ok := StaticBuildCmd(app, src, nil)
	assert.False(t, ok, "sem package.json e sem comando: sem build")

	app.StaticBuildCmd, app.StaticBaseDir, app.StaticImage = "make site", "web", "node:20"
	c, ok := StaticBuildCmd(app, src, []string{"A=1", "B=x=y"})
	require.True(t, ok)
	assert.Equal(t, []string{"run", "--rm", "--cpus", "2", "--memory", "2g", "-v", src + ":/app",
		"-w", "/app/web", "-e", "A", "-e", "B", "node:20", "sh", "-c", "make site"}, c.Args)
	assert.Equal(t, []string{"A=1", "B=x=y"}, c.Env)
}

func TestStaticPublishRoot(t *testing.T) {
	assert.Equal(t, filepath.Join(wwwRoot, "x"), StaticPublishRoot(store.App{Slug: "x"}))
	assert.Equal(t, "/var/www/y", StaticPublishRoot(store.App{Slug: "x", PublishRoot: "/var/www/y"}))
}

func TestStatic_CancelAfterPublishRemovesRelease(t *testing.T) {
	linuxOnly(t)
	h, root := staticHarness(t, map[string]string{"package.json": "{}"}, nil)
	var built atomic.Bool
	var id atomic.Int64
	buildWrites(t, h, func(context.Context) error { built.Store(true); return nil })
	// O primeiro s.now() depois do build é o do nome da versão: cancela ali, e
	// o cancelamento só é visto depois de a versão estar publicada.
	h.svc.Now = func() time.Time {
		if built.CompareAndSwap(true, false) {
			for id.Load() == 0 {
				time.Sleep(time.Millisecond)
			}
			_ = h.svc.Cancel(h.ctx, id.Load())
		}
		return testNow
	}
	old := seedRelease(t, h, root, "20261101000000-1111111", true)

	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	id.Store(d.ID)
	got := h.wait(d.ID)

	assert.Equal(t, "cancelled", got.Status)
	assert.Equal(t, old.ReleaseDir, currentOf(t, root))
	assert.Equal(t, []string{"20261101000000-1111111"}, releasesIn(t, root))
}
