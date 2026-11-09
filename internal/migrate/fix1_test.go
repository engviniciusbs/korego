package migrate

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/proxy"
)

func TestStartTakeover_PanicInPreconditionsResumes(t *testing.T) {
	h := newHarness(t)
	h.sd.PanicExists = true
	assert.Panics(t, func() { _ = h.e.StartTakeover(context.Background(), nil) })
	assert.False(t, h.dom.paused, "a pausa é desfeita mesmo com pânico")
	require.Len(t, h.dom.resumed, 1)
	assert.False(t, h.e.Running())
	_, ok := h.setting(t, SettingInProgress)
	assert.False(t, ok)
}

func TestTakeover_PanicInJobSettles(t *testing.T) {
	h := newHarness(t)
	base := h.e.Check
	h.e.Check = func(ctx context.Context, sites []proxy.Site, addr string, trial bool) []SiteCheck {
		if addr == NormalAddr {
			panic("verificação quebrou")
		}
		return base(ctx, sites, addr, trial)
	}
	res := h.e.Takeover(context.Background())
	assert.Equal(t, OutcomeFailed, res.Outcome)
	assert.Contains(t, res.Message, "erro interno")
	assert.False(t, h.dom.paused)
	assert.False(t, h.e.Running())
	_, ok := h.setting(t, SettingInProgress)
	assert.False(t, ok)
	// Caddy servindo e nginx parado: Voltar fica disponível.
	from, ok := h.setting(t, SettingMigratedFrom)
	assert.True(t, ok)
	assert.Equal(t, "nginx", from)
	assert.Contains(t, res.Message, "Caddy está servindo 80/443")
}

func TestTakeover_InProgressMarkerDuringJob(t *testing.T) {
	h := newHarness(t)
	var seen string
	base := h.e.Check
	h.e.Check = func(ctx context.Context, sites []proxy.Site, addr string, trial bool) []SiteCheck {
		if trial {
			seen, _ = h.setting(t, SettingInProgress)
		}
		return base(ctx, sites, addr, trial)
	}
	require.Equal(t, OutcomeOK, h.e.Takeover(context.Background()).Outcome)
	var m InProgress
	require.NoError(t, json.Unmarshal([]byte(seen), &m))
	assert.Equal(t, OpTakeover, m.Op)
	assert.Equal(t, "nginx", m.Unit)
	assert.Equal(t, h.start.Unix(), m.StartedAt.Unix())
	_, ok := h.setting(t, SettingInProgress)
	assert.False(t, ok)
}

func setMarker(t *testing.T, h *harness, op string) {
	t.Helper()
	b, err := json.Marshal(InProgress{Op: op, Unit: "nginx", StartedAt: h.start})
	require.NoError(t, err)
	require.NoError(t, h.db.SetSetting(context.Background(), SettingInProgress, string(b)))
}

func TestRecoverInterrupted_CaddyServingOffersRollback(t *testing.T) {
	h := newHarness(t)
	setMarker(t, h, OpTakeover)
	h.sd.active["nginx"] = false // o KoreGO caiu depois do stop
	reconciled := 0
	h.dom.reconcile = func(context.Context) proxy.Status {
		reconciled++
		return proxy.Status{State: proxy.StateActive, Mode: proxy.ListenNormal}
	}
	require.NoError(t, h.e.RecoverInterrupted(context.Background()))
	assert.Equal(t, 1, reconciled)

	info, err := h.e.Info(context.Background())
	require.NoError(t, err)
	require.NotNil(t, info.Last)
	assert.Equal(t, OpTakeover, info.Last.Op)
	assert.Equal(t, OutcomeFailed, info.Last.Outcome)
	assert.Contains(t, info.Last.Message, "interrompida (KoreGO reiniciou durante a troca)")
	assert.Equal(t, "nginx", info.MigratedFrom)
	assert.True(t, info.RollbackAvailable)
	_, ok := h.setting(t, SettingInProgress)
	assert.False(t, ok)
	assert.Empty(t, h.c.list(), "não mexe em systemd nem no proxy além da reconciliação")
}

func TestRecoverInterrupted_NginxBackNoRollback(t *testing.T) {
	h := newHarness(t)
	setMarker(t, h, OpTakeover)
	h.dom.reconcile = func(context.Context) proxy.Status {
		return proxy.Status{State: proxy.StateTrial, Mode: proxy.ListenTrial, Process: "nginx (pid 7)"}
	}
	require.NoError(t, h.e.RecoverInterrupted(context.Background()))
	_, ok := h.setting(t, SettingMigratedFrom)
	assert.False(t, ok)
	info, err := h.e.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, OutcomeFailed, info.Last.Outcome)
	_, ok = h.setting(t, SettingInProgress)
	assert.False(t, ok)

	// Sem marca: não faz nada.
	h.dom.reconcile = func(context.Context) proxy.Status { t.Fatal("não deveria reconciliar"); return proxy.Status{} }
	require.NoError(t, h.e.RecoverInterrupted(context.Background()))
}

func TestTakeover_DNSNotReady409(t *testing.T) {
	h := newHarness(t)
	h.dom.sites = []proxy.Site{
		site("exemplo.com", proxy.TLSAuto),
		site("loja.exemplo.net", proxy.TLSAuto),
		site("painel.exemplo.com", proxy.TLSCustom), // próprio: DNS não importa
	}
	h.dom.dns = map[string]bool{"loja.exemplo.net": false, "painel.exemplo.com": false}
	err := h.e.StartTakeover(context.Background(), nil)
	var ref *Refusal
	require.ErrorAs(t, err, &ref)
	assert.Equal(t, CodeDNSNotReady, ref.Code)
	assert.Contains(t, ref.Message, "loja.exemplo.net")
	assert.NotContains(t, ref.Message, "painel.exemplo.com")
	assert.NotContains(t, ref.Message, "exemplo.com,")
	assert.Empty(t, h.c.list())
	assert.False(t, h.e.Running())
	assert.False(t, h.dom.paused)
}

func TestPrecheck_CheapRefusals(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, h.e.PrecheckTakeover(context.Background()))
	var ref *Refusal
	require.ErrorAs(t, h.e.PrecheckRollback(context.Background()), &ref)
	assert.Equal(t, CodeNotMigrated, ref.Code)

	h.dom.status = proxy.Status{State: proxy.StateActive, Mode: proxy.ListenNormal}
	require.ErrorAs(t, h.e.PrecheckTakeover(context.Background()), &ref)
	assert.Equal(t, CodeNotTrial, ref.Code)
	h.dom.status = proxy.Status{State: proxy.StateTrial, Process: "haproxy (pid 1)"}
	require.ErrorAs(t, h.e.PrecheckTakeover(context.Background()), &ref)
	assert.Equal(t, CodeUnsupportedOwner, ref.Code)
	assert.Equal(t, 0, h.dom.pauses, "pré-checagem não pausa")
}

func TestInfo_ExistsCached60s(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, h.db.SetSetting(context.Background(), SettingMigratedFrom, "nginx"))
	for i := 0; i < 3; i++ {
		info, err := h.e.Info(context.Background())
		require.NoError(t, err)
		assert.True(t, info.RollbackAvailable)
	}
	assert.Equal(t, 1, h.sd.existsCalls)
	_ = h.clk.Sleep(context.Background(), 61*time.Second)
	_, err := h.e.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, h.sd.existsCalls)
}

func TestUpdateBlocksMigrationAndViceVersa(t *testing.T) {
	h := newHarness(t)
	release, err := h.e.BlockForUpdate()
	require.NoError(t, err)
	var ref *Refusal
	require.ErrorAs(t, h.e.StartTakeover(context.Background(), nil), &ref)
	assert.Equal(t, CodeUpdating, ref.Code)
	require.ErrorAs(t, h.e.PrecheckTakeover(context.Background()), &ref)
	assert.Equal(t, CodeUpdating, ref.Code)
	assert.False(t, h.e.Running())
	release()
	require.NoError(t, h.e.PrecheckTakeover(context.Background()))

	// Troca em andamento: a atualização é recusada.
	entered, cont := make(chan struct{}), make(chan struct{})
	base := h.e.Check
	h.e.Check = func(ctx context.Context, sites []proxy.Site, addr string, trial bool) []SiteCheck {
		if trial {
			close(entered)
			<-cont
		}
		return base(ctx, sites, addr, trial)
	}
	done := make(chan Result, 1)
	require.NoError(t, h.e.StartTakeover(context.Background(), func(r Result) { done <- r }))
	<-entered
	_, err = h.e.BlockForUpdate()
	require.ErrorAs(t, err, &ref)
	assert.Equal(t, CodeRunning, ref.Code)
	close(cont)
	<-done
}
