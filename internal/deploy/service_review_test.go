package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Queda no meio da troca: o deploy já é o atual da app → volta a running, e o
// anterior (que ainda estava running) sai.
func TestRecover_CurrentInterruptedPromoted(t *testing.T) {
	h := newHarness(t, nil)
	prev := h.seedCurrent(20000, "c-prev")
	d := store.Deploy{AppID: h.app.ID, Trigger: "manual", Status: "starting", HostPort: 20001,
		ContainerID: "c-new", Image: "korego/blog:abcdef1"}
	require.NoError(t, h.db.InsertDeploy(h.ctx, &d))
	require.NoError(t, h.db.SetCurrentDeploy(h.ctx, h.app.ID, d.ID))
	a := h.getApp()
	a.HostPort = 20001
	require.NoError(t, h.db.UpdateApp(h.ctx, &a))

	// log aberto, sem marcador de fim
	require.NoError(t, os.MkdirAll(filepath.Join(h.svc.DataDir, "logs"), 0o700))
	require.NoError(t, os.WriteFile(logPath(h.svc.DataDir, d.ID), []byte("==> Trocando a rota\n"), 0o600))
	require.False(t, LogFinished(h.svc.DataDir, d.ID))

	h.mu.Lock()
	h.labeled["korego.deploy="+fmt.Sprint(prev.ID)] = []docker.Container{{ID: "c-prev"}}
	h.labeled["korego.deploy="+fmt.Sprint(d.ID)] = []docker.Container{{ID: "c-new"}}
	h.mu.Unlock()

	require.NoError(t, h.svc.Recover(h.ctx))

	gd, err := h.db.GetDeploy(h.ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, "running", gd.Status)
	assert.Equal(t, "", gd.Reason)
	gp, err := h.db.GetDeploy(h.ctx, prev.ID)
	require.NoError(t, err)
	assert.Equal(t, "superseded", gp.Status)

	assert.GreaterOrEqual(t, h.rec.first("remove:c-prev"), 0)
	assert.Equal(t, -1, h.rec.first("remove:c-new"), "o container no ar fica")
	assert.Equal(t, 20001, h.getApp().HostPort)
	assert.Equal(t, d.ID, *h.getApp().CurrentDeployID)
	assert.True(t, LogFinished(h.svc.DataDir, d.ID), "Recover fecha o log do interrompido")
	assert.Eventually(t, func() bool {
		s := h.statusesOf(d.ID)
		return len(s) > 0 && s[len(s)-1] == "running"
	}, time.Second, 5*time.Millisecond)
}

func TestDeploy_ComposeFirstFailResetsPort(t *testing.T) {
	h := newHarness(t, asCompose)
	d1, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	g1 := h.wait(d1.ID)
	assert.Equal(t, "failed", g1.Status)
	assert.Equal(t, "container do serviço web não encontrado", g1.Reason)
	assert.Equal(t, 0, h.getApp().HostPort, "porta liberada se o primeiro deploy falha")

	h.mu.Lock()
	h.labeled["com.docker.compose.project=korego-blog"] = []docker.Container{{ID: "c-web", Service: "web"}}
	h.mu.Unlock()
	d2, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	g2 := h.wait(d2.ID)
	require.Equal(t, "running", g2.Status, g2.Reason)
	assert.Equal(t, 20000, h.getApp().HostPort)
}

// Dois deploys de apps diferentes escolhendo porta ao mesmo tempo nunca
// recebem a mesma. O listen falso segura a primeira chamada até a segunda
// chegar (ou 300 ms): sem a trava, as duas escolheriam 20000.
func TestDeploy_PortReservationConcurrent(t *testing.T) {
	for _, compose := range []bool{false, true} {
		t.Run(fmt.Sprintf("compose=%v", compose), func(t *testing.T) {
			mut := func(*store.App) {}
			if compose {
				mut = asCompose
			}
			h := newHarness(t, mut)
			other := h.app
			other.ID, other.Slug, other.HostPort = 0, "outra", 0
			require.NoError(t, h.db.InsertApp(h.ctx, &other))
			h.mu.Lock()
			h.labeled["com.docker.compose.project=korego-blog"] = []docker.Container{{ID: "c-blog", Service: "web"}}
			h.labeled["com.docker.compose.project=korego-outra"] = []docker.Container{{ID: "c-outra", Service: "web"}}
			h.mu.Unlock()

			var mu sync.Mutex
			arrived := 0
			h.svc.listen = func(string) error {
				mu.Lock()
				arrived++
				mu.Unlock()
				deadline := time.Now().Add(300 * time.Millisecond)
				for time.Now().Before(deadline) {
					mu.Lock()
					n := arrived
					mu.Unlock()
					if n >= 2 {
						break
					}
					time.Sleep(time.Millisecond)
				}
				return nil
			}

			d1, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
			require.NoError(t, err)
			d2, err := h.svc.Deploy(h.ctx, other.ID, "manual", "", "")
			require.NoError(t, err)
			g1, g2 := h.wait(d1.ID), h.wait(d2.ID)
			require.Equal(t, "running", g1.Status, g1.Reason)
			require.Equal(t, "running", g2.Status, g2.Reason)
			assert.NotEqual(t, g1.HostPort, g2.HostPort)
			a1, err := h.db.GetApp(h.ctx, h.app.ID)
			require.NoError(t, err)
			a2, err := h.db.GetApp(h.ctx, other.ID)
			require.NoError(t, err)
			assert.NotEqual(t, a1.HostPort, a2.HostPort)
		})
	}
}

func TestDeploy_ProxyInactiveReason(t *testing.T) {
	h := newHarness(t, nil)
	h.seedCurrent(20000, "old-cid")
	h.router.errFn = func(int) error { return proxy.ErrInactive }
	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)
	assert.Equal(t, "failed", got.Status)
	assert.Equal(t, "proxy inativo: as portas 80/443 estão em uso ou o Caddy não está ativo", got.Reason)
	assert.Equal(t, 20000, h.getApp().HostPort)
	assert.Equal(t, -1, h.rec.first("remove:old-cid"))
}

func TestDeploy_PanicMarksFailed(t *testing.T) {
	h := newHarness(t, nil)
	h.setHealth(func(string) docker.Details { panic("boom") })
	d, err := h.svc.Deploy(h.ctx, h.app.ID, "manual", "", "")
	require.NoError(t, err)
	got := h.wait(d.ID)
	assert.Equal(t, "failed", got.Status)
	assert.Equal(t, "erro interno no deploy", got.Reason)
	assert.Equal(t, 1, h.rec.count("remove:cid-"+fmt.Sprint(d.ID)), "container novo removido")
	_, err = os.Stat(filepath.Join(h.svc.DataDir, "builds", fmt.Sprint(d.ID)))
	assert.True(t, os.IsNotExist(err))
	entries, err := ReadLog(h.svc.DataDir, d.ID, 0)
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	assert.True(t, strings.HasSuffix(entries[len(entries)-1].Text, "erro interno no deploy"))

	// a fila continua funcionando
	h.setHealth(func(string) docker.Details { return det("running", "healthy") })
	d2, err := h.svc.Deploy(context.Background(), h.app.ID, "manual", "", "")
	require.NoError(t, err)
	assert.Equal(t, "running", h.wait(d2.ID).Status)
}
