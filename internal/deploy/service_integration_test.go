//go:build integration && linux

package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/jobs"
	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/secrets"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// portRouter faz o papel do proxy: guarda a porta da app a cada ReapplyApp.
// get segura o RLock durante a requisição, então a troca espera a requisição
// em andamento terminar, como um proxy que drena conexões.
type portRouter struct {
	db    *store.DB
	mu    sync.RWMutex
	port  int
	calls []int
}

func (r *portRouter) ReapplyApp(ctx context.Context, appID int64) error {
	a, err := r.db.GetApp(ctx, appID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.port = a.HostPort
	r.calls = append(r.calls, a.HostPort)
	return nil
}

func (r *portRouter) get(c *http.Client) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.port == 0 {
		return "", errors.New("sem rota")
	}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/", r.port))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	return string(b), nil
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=korego", "-c", "user.email=ci@exemplo.com"}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	require.NoError(t, err, string(out))
}

func commitFile(t *testing.T, repo, name, content, msg string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644))
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", msg)
}

func waitDeploy(t *testing.T, db *store.DB, id int64, timeout time.Duration) store.Deploy {
	t.Helper()
	var d store.Deploy
	require.Eventually(t, func() bool {
		var err error
		d, err = db.GetDeploy(context.Background(), id)
		require.NoError(t, err)
		return d.Status == "running" || d.Status == "failed" || d.Status == "cancelled" || d.Status == "superseded"
	}, timeout, 200*time.Millisecond, "deploy %d não terminou", id)
	return d
}

func TestIntegration_DeployZeroDowntimeAndHealthFail(t *testing.T) {
	ctx := context.Background()
	dock := docker.NewLocal()
	require.NoError(t, dock.Ping(ctx))
	slug := fmt.Sprintf("itest-%d", time.Now().UnixNano()%1_000_000)

	// Limpeza final (registrada antes da fila: roda depois do Close).
	t.Cleanup(func() {
		cs, _ := dock.ListByLabel(ctx, "korego.app", slug)
		for _, c := range cs {
			_ = dock.RemoveForce(ctx, c.ID)
		}
		imgs, _ := dock.Images(ctx, "korego/"+slug)
		for _, img := range imgs {
			_ = dock.RemoveImage(ctx, img.Ref)
		}
	})

	// Repositório git local a partir de testdata/sample-app.
	repo := t.TempDir()
	src := filepath.Join("..", "..", "testdata", "sample-app")
	for _, name := range []string{"Dockerfile", "index.html"} {
		b, err := os.ReadFile(filepath.Join(src, name))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(repo, name), b, 0o644))
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "primeira versão")

	db := store.OpenMemory(t)
	p := &store.Project{Name: "integração"}
	require.NoError(t, db.InsertProject(ctx, p))
	app := store.App{ProjectID: p.ID, Slug: slug, SourceType: "git", Repo: "file://" + repo,
		BuildType: "dockerfile", Branch: "main", DockerfilePath: "Dockerfile", InternalPort: 80, HealthTimeoutS: 90}
	require.NoError(t, db.InsertApp(ctx, &app))

	box, err := secrets.NewBox(make([]byte, 32))
	require.NoError(t, err)
	router := &portRouter{db: db}
	dataDir := t.TempDir()
	q := jobs.NewQueue(2)
	t.Cleanup(q.Close)
	svc := &Service{DB: db, Docker: dock, Runner: ExecRunner{}, Box: box, Hub: events.NewHub(), Queue: q,
		Router: router, DataDir: dataDir, AllowFileURLs: true}

	logOf := func(id int64) string {
		entries, _ := ReadLog(dataDir, id, 0)
		var b strings.Builder
		for _, e := range entries {
			b.WriteString(e.Text + "\n")
		}
		return b.String()
	}

	// 1º deploy
	d1, err := svc.Deploy(ctx, app.ID, "manual", "", "")
	require.NoError(t, err)
	g1 := waitDeploy(t, db, d1.ID, 5*time.Minute)
	require.Equal(t, "running", g1.Status, "%s\n%s", g1.Reason, logOf(d1.ID))

	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	body, err := router.get(client)
	require.NoError(t, err)
	assert.Contains(t, body, "korego-sample")

	// 2º deploy com página nova e polling contínuo durante a troca
	var oks, fails atomic.Int64
	var lastErr atomic.Value
	stop := make(chan struct{})
	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			b, err := router.get(client)
			if err == nil && !strings.Contains(b, "korego-sample") {
				err = fmt.Errorf("resposta inesperada: %q", b)
			}
			if err != nil {
				fails.Add(1)
				lastErr.Store(err.Error())
			} else {
				oks.Add(1)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	commitFile(t, repo, "index.html", "<!doctype html>\n<p>korego-sample v2</p>\n", "segunda versão")
	d2, err := svc.Deploy(ctx, app.ID, "manual", "", "")
	require.NoError(t, err)
	g2 := waitDeploy(t, db, d2.ID, 5*time.Minute)
	require.Equal(t, "running", g2.Status, "%s\n%s", g2.Reason, logOf(d2.ID))
	assert.NotEqual(t, g1.HostPort, g2.HostPort)
	assert.Eventually(t, func() bool {
		b, err := router.get(client)
		return err == nil && strings.Contains(b, "v2")
	}, 10*time.Second, 100*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	close(stop)
	<-pollDone
	assert.Zero(t, fails.Load(), "requisições falharam na troca (última: %v)", lastErr.Load())
	assert.Greater(t, oks.Load(), int64(10))

	prev, err := db.GetDeploy(ctx, d1.ID)
	require.NoError(t, err)
	assert.Equal(t, "superseded", prev.Status)
	_, err = dock.Inspect(ctx, g1.ContainerID)
	assert.ErrorIs(t, err, docker.ErrNotFound, "container anterior removido")

	// 3º deploy com HEALTHCHECK impossível: falha e o 2º segue no ar
	df, err := os.ReadFile(filepath.Join(repo, "Dockerfile"))
	require.NoError(t, err)
	broken := strings.Replace(string(df),
		"HEALTHCHECK --interval=2s --timeout=2s --retries=5 CMD wget -qO- http://127.0.0.1/ || exit 1",
		"HEALTHCHECK --interval=1s --timeout=1s --retries=2 CMD exit 1", 1)
	require.NotEqual(t, string(df), broken)
	commitFile(t, repo, "Dockerfile", broken, "healthcheck quebrado")
	d3, err := svc.Deploy(ctx, app.ID, "manual", "", "")
	require.NoError(t, err)
	g3 := waitDeploy(t, db, d3.ID, 5*time.Minute)
	require.Equal(t, "failed", g3.Status, logOf(d3.ID))
	assert.True(t, strings.HasPrefix(g3.Reason, "verificação de saúde falhou"), g3.Reason)

	body, err = router.get(client)
	require.NoError(t, err)
	assert.Contains(t, body, "v2")
	a, err := db.GetApp(ctx, app.ID)
	require.NoError(t, err)
	require.NotNil(t, a.CurrentDeployID)
	assert.Equal(t, d2.ID, *a.CurrentDeployID)
	assert.Equal(t, g2.HostPort, a.HostPort)
	cs, err := dock.ListByLabel(ctx, "korego.deploy", fmt.Sprint(d3.ID))
	require.NoError(t, err)
	assert.Empty(t, cs, "container do deploy com falha removido")
	router.mu.RLock()
	assert.Equal(t, []int{g1.HostPort, g2.HostPort}, router.calls)
	router.mu.RUnlock()
}
