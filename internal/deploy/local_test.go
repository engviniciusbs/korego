package deploy

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/docker/dockertest"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stackLabels(dir, files string) map[string]string {
	return map[string]string{
		"com.docker.compose.project":              "loja",
		"com.docker.compose.project.working_dir":  dir,
		"com.docker.compose.project.config_files": files,
	}
}

func listFake(cs []docker.Container, err error) *dockertest.Fake {
	return &dockertest.Fake{ListByLabelFn: func(_ context.Context, key, value string) ([]docker.Container, error) {
		if key != "com.docker.compose.project" || value != "loja" {
			return nil, nil
		}
		return cs, err
	}}
}

func TestFindStack(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	dirOld, dirNew, dirManaged := filepath.Join(base, "old"), filepath.Join(base, "new"), filepath.Join(base, "managed")
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	newYml := filepath.Join(dirNew, "compose.yml")

	t.Run("o mais novo e os arquivos", func(t *testing.T) {
		d := listFake([]docker.Container{
			{Name: "loja-db-1", CreatedAt: t0, Labels: stackLabels(dirOld, "compose.yml")},
			{Name: "loja-web-1", CreatedAt: t0.Add(time.Hour), Labels: stackLabels(dirNew, " "+newYml+" , override.yml,,")},
			{Name: "korego-x", CreatedAt: t0.Add(2 * time.Hour), Managed: true, Labels: stackLabels(dirManaged, "c.yml")},
		}, nil)
		st, err := FindStack(ctx, d, "loja")
		require.NoError(t, err)
		assert.Equal(t, "loja", st.Project)
		assert.Equal(t, dirNew, st.Dir)
		assert.Equal(t, []string{newYml, "override.yml"}, st.Files)
		assert.Len(t, st.Containers, 2, "o container do KoreGO fica de fora")
	})
	t.Run("empate pelo nome", func(t *testing.T) {
		d := listFake([]docker.Container{
			{Name: "loja-b", CreatedAt: t0, Labels: stackLabels(dirOld, "b.yml")},
			{Name: "loja-a", CreatedAt: t0, Labels: stackLabels(dirNew, "a.yml")},
		}, nil)
		st, err := FindStack(ctx, d, "loja")
		require.NoError(t, err)
		assert.Equal(t, dirNew, st.Dir)
		assert.Equal(t, []string{"a.yml"}, st.Files)
	})
	t.Run("sem containers", func(t *testing.T) {
		_, err := FindStack(ctx, listFake(nil, nil), "loja")
		assert.ErrorIs(t, err, ErrStackNotFound)
		_, err = FindStack(ctx, listFake([]docker.Container{{Name: "k", Managed: true, Labels: stackLabels(dirNew, "a.yml")}}, nil), "loja")
		assert.ErrorIs(t, err, ErrStackNotFound, "só containers do KoreGO")
		_, err = FindStack(ctx, listFake(nil, nil), "")
		assert.ErrorIs(t, err, ErrStackNotFound)
	})
	t.Run("rótulos ausentes", func(t *testing.T) {
		for _, l := range []map[string]string{
			nil,
			{"com.docker.compose.project.working_dir": dirNew},
			{"com.docker.compose.project.config_files": "a.yml"},
			stackLabels(dirNew, " , "),
			stackLabels("relativa", "a.yml"),
		} {
			_, err := FindStack(ctx, listFake([]docker.Container{{Name: "loja-web-1", Labels: l}}, nil), "loja")
			assert.ErrorIs(t, err, ErrStackLabels, "%v", l)
		}
	})
	t.Run("erro do docker", func(t *testing.T) {
		_, err := FindStack(ctx, listFake(nil, docker.ErrUnavailable), "loja")
		assert.ErrorIs(t, err, docker.ErrUnavailable)
	})
}

func TestValidUpdateScript(t *testing.T) {
	base := t.TempDir()
	stack := filepath.Join(base, "stack")
	require.NoError(t, os.MkdirAll(filepath.Join(stack, "sub"), 0o755))
	wf(t, filepath.Join(stack, "update.sh"), "x")
	wf(t, filepath.Join(base, "fora.sh"), "x")

	got, err := ValidUpdateScript(stack, "update.sh")
	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(filepath.Join(stack, "update.sh"))
	require.NoError(t, err)
	assert.Equal(t, want, got)
	_, err = ValidUpdateScript(stack, filepath.Join(stack, "update.sh"))
	assert.NoError(t, err, "absoluto dentro da pasta")

	for _, bad := range []string{"", "../fora.sh", filepath.Join(base, "fora.sh"), "sub", "nao-existe.sh"} {
		_, err := ValidUpdateScript(stack, bad)
		assert.Error(t, err, bad)
	}
	require.NoError(t, os.Symlink(filepath.Join(base, "fora.sh"), filepath.Join(stack, "link.sh")))
	_, err = ValidUpdateScript(stack, "link.sh")
	assert.Error(t, err, "link que sai da pasta")
}

func TestStackEnvPath(t *testing.T) {
	assert.Equal(t, filepath.Join("srv", "loja", ".env"), StackEnvPath(Stack{Dir: filepath.Join("srv", "loja")}))
}

// localHarness monta uma app compose-local com a stack "loja" na pasta
// devolvida (arquivos compose.yml e override.yml).
func localHarness(t *testing.T, mut func(a *store.App)) (*harness, string) {
	t.Helper()
	dir := t.TempDir()
	h := newHarness(t, func(a *store.App) {
		a.SourceType, a.BuildType, a.Repo, a.Branch, a.DockerfilePath = "local", "compose-local", "", "", ""
		a.ComposeProject, a.UpdateMode = "loja", "pull"
		if mut != nil {
			mut(a)
		}
	})
	h.mu.Lock()
	h.labeled["com.docker.compose.project=loja"] = []docker.Container{
		{ID: "c-web", Name: "loja-web-1", Project: "loja", CreatedAt: testNow,
			Labels: stackLabels(dir, filepath.Join(dir, "compose.yml")+",override.yml")},
	}
	h.mu.Unlock()
	// Variável da app: não pode ir para os comandos da stack.
	enc, err := h.box.Encrypt([]byte("valor-secreto"))
	require.NoError(t, err)
	require.NoError(t, h.db.SetAppEnv(h.ctx, h.app.ID, map[string]string{"SEGREDO": enc}, nil))
	return h, dir
}

func composeArgs(dir string, op ...string) []string {
	return append([]string{"compose", "-p", "loja", "-f", filepath.Join(dir, "compose.yml"), "-f", "override.yml"}, op...)
}

func TestStackAction_Commands(t *testing.T) {
	h, dir := localHarness(t, nil)
	for i, op := range []string{"start", "stop", "restart"} {
		require.NoError(t, h.svc.StackAction(h.ctx, h.app.ID, op))
		cmds := h.runner.list()
		require.Len(t, cmds, i+1)
		c := cmds[i]
		assert.Equal(t, "docker", c.Name)
		assert.Equal(t, composeArgs(dir, op), c.Args)
		assert.Equal(t, dir, c.Dir)
		assert.Empty(t, c.Env, "sem variáveis da app")
	}
	n, err := h.db.ListDeploys(h.ctx, h.app.ID, 10)
	require.NoError(t, err)
	assert.Empty(t, n, "ação na stack não cria deploy")

	assert.Error(t, h.svc.StackAction(h.ctx, h.app.ID, "down"))
	assert.Len(t, h.runner.list(), 3)

	h.runner.hook = func(_ context.Context, _ Cmd, out io.Writer) (bool, error) {
		_, _ = io.WriteString(out, "no such service: web\n")
		return true, errors.New("exit status 1")
	}
	err = h.svc.StackAction(h.ctx, h.app.ID, "restart")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such service: web")

	h.mu.Lock()
	h.labeled["com.docker.compose.project=loja"] = nil
	h.mu.Unlock()
	assert.ErrorIs(t, h.svc.StackAction(h.ctx, h.app.ID, "start"), ErrStackNotFound)
}

func TestStackAction_NotLocal(t *testing.T) {
	h := newHarness(t, nil)
	assert.Error(t, h.svc.StackAction(h.ctx, h.app.ID, "start"))
	assert.Empty(t, h.runner.list())
}

// seedLocalCurrent grava um deploy running como o atual da app.
func seedLocalCurrent(t *testing.T, h *harness) store.Deploy {
	t.Helper()
	d := store.Deploy{AppID: h.app.ID, Trigger: "manual", Status: "running"}
	require.NoError(t, h.db.InsertDeploy(h.ctx, &d))
	require.NoError(t, h.db.SetCurrentDeploy(h.ctx, h.app.ID, d.ID))
	return d
}

func TestLocalUpdate_Pull(t *testing.T) {
	h, dir := localHarness(t, nil)
	prev := seedLocalCurrent(t, h)
	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)
	require.Equal(t, "running", got.Status, got.Reason)

	cmds := h.runner.list()
	require.Len(t, cmds, 2)
	assert.Equal(t, composeArgs(dir, "pull"), cmds[0].Args)
	assert.Equal(t, composeArgs(dir, "up", "-d"), cmds[1].Args)
	for _, c := range cmds {
		assert.Equal(t, "docker", c.Name)
		assert.Equal(t, dir, c.Dir)
		assert.Empty(t, c.Env, "sem variáveis da app")
	}
	assert.Zero(t, got.HostPort)
	assert.Empty(t, got.Image)
	assert.Equal(t, d.ID, *h.getApp().CurrentDeployID)
	assert.Equal(t, -1, h.rec.first("reapply:"), "sem proxy")
	p, err := h.db.GetDeploy(h.ctx, prev.ID)
	require.NoError(t, err)
	assert.Equal(t, "superseded", p.Status)
}

func TestLocalUpdate_PullFails(t *testing.T) {
	h, _ := localHarness(t, nil)
	h.runner.hook = func(_ context.Context, c Cmd, _ io.Writer) (bool, error) {
		return true, errors.New("exit status 1")
	}
	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)
	assert.Equal(t, "failed", got.Status)
	assert.Contains(t, got.Reason, "pull")
	assert.Len(t, h.runner.list(), 1, "up não roda depois de um pull que falhou")
	assert.Nil(t, h.getApp().CurrentDeployID)
}

func TestLocalUpdate_Script(t *testing.T) {
	h, dir := localHarness(t, func(a *store.App) { a.UpdateMode, a.UpdateScript = "script", "update.sh" })
	wf(t, filepath.Join(dir, "update.sh"), "#!/bin/sh\n")
	want, err := filepath.EvalSymlinks(filepath.Join(dir, "update.sh"))
	require.NoError(t, err)

	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)
	require.Equal(t, "running", got.Status, got.Reason)

	cmds := h.runner.list()
	require.Len(t, cmds, 1)
	assert.Equal(t, want, cmds[0].Name)
	assert.Empty(t, cmds[0].Args)
	assert.Equal(t, dir, cmds[0].Dir)
	assert.Empty(t, cmds[0].Env)
	assert.Equal(t, d.ID, *h.getApp().CurrentDeployID)
}

func TestLocalUpdate_ScriptEscapes(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "fora.sh")
	wf(t, outside, "#!/bin/sh\n")
	for name, script := range map[string]string{"link": "update.sh", "relativo": "../fora.sh"} {
		t.Run(name, func(t *testing.T) {
			h, dir := localHarness(t, func(a *store.App) { a.UpdateMode, a.UpdateScript = "script", script })
			if name == "link" {
				require.NoError(t, os.Symlink(outside, filepath.Join(dir, "update.sh")))
			} else {
				wf(t, filepath.Join(filepath.Dir(dir), "fora.sh"), "#!/bin/sh\n")
			}
			d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
			require.NoError(t, err)
			got := h.wait(d.ID)
			assert.Equal(t, "failed", got.Status)
			assert.Contains(t, got.Reason, "script de atualização inválido")
			assert.Empty(t, h.runner.list(), "nada executado")
		})
	}
}

func TestLocalUpdate_Cancel(t *testing.T) {
	h, _ := localHarness(t, nil)
	started := make(chan struct{})
	h.runner.hook = func(ctx context.Context, c Cmd, _ io.Writer) (bool, error) {
		close(started)
		<-ctx.Done()
		return true, ctx.Err()
	}
	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("pull não começou")
	}
	require.NoError(t, h.svc.Cancel(h.ctx, d.ID))
	got := h.wait(d.ID)
	assert.Equal(t, "cancelled", got.Status)
	assert.Len(t, h.runner.list(), 1, "up não roda depois do cancelamento")
	assert.Nil(t, h.getApp().CurrentDeployID)
}

func TestLocalUpdate_StackGone(t *testing.T) {
	h, _ := localHarness(t, nil)
	h.mu.Lock()
	h.labeled["com.docker.compose.project=loja"] = []docker.Container{{ID: "c", Name: "loja-web-1"}}
	h.mu.Unlock()
	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)
	assert.Equal(t, "failed", got.Status)
	assert.Contains(t, got.Reason, "rótulos")
	assert.Empty(t, h.runner.list())
}

func TestRollback_LocalUnsupported(t *testing.T) {
	h, _ := localHarness(t, nil)
	prev := seedLocalCurrent(t, h)
	_, err := h.svc.Rollback(h.ctx, h.app.ID, prev.ID)
	assert.ErrorIs(t, err, ErrRollbackUnsupported)
}

func TestRecover_LocalInterrupted(t *testing.T) {
	h, _ := localHarness(t, nil)
	prev := seedLocalCurrent(t, h)
	d := store.Deploy{AppID: h.app.ID, Trigger: "manual", Status: "starting"}
	require.NoError(t, h.db.InsertDeploy(h.ctx, &d))

	require.NoError(t, h.svc.Recover(h.ctx))

	got, err := h.db.GetDeploy(h.ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", got.Status)
	assert.Equal(t, "interrompido", got.Reason)
	p, err := h.db.GetDeploy(h.ctx, prev.ID)
	require.NoError(t, err)
	assert.Equal(t, "running", p.Status)
	assert.Equal(t, prev.ID, *h.getApp().CurrentDeployID)
	assert.Equal(t, -1, h.rec.first("remove:"), "a stack não é tocada")
	assert.Empty(t, h.runner.list())
	assert.True(t, LogFinished(h.svc.DataDir, d.ID))
}

func TestRecover_LocalInterruptedAfterRecord(t *testing.T) {
	// Caiu entre record e markRunning: mesmo sendo o atual, fica failed.
	h, _ := localHarness(t, nil)
	d := store.Deploy{AppID: h.app.ID, Trigger: "manual", Status: "starting"}
	require.NoError(t, h.db.InsertDeploy(h.ctx, &d))
	require.NoError(t, h.db.SetCurrentDeploy(h.ctx, h.app.ID, d.ID))

	require.NoError(t, h.svc.Recover(h.ctx))

	got, err := h.db.GetDeploy(h.ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", got.Status)
	assert.Empty(t, h.runner.list())
}
