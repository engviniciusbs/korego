package migrate

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/proxy"
)

func TestUnitForProcess(t *testing.T) {
	for in, want := range map[string]string{
		"nginx (pid 1402285)": "nginx",
		"apache2 (pid 12)":    "apache2",
		"apache (pid 12)":     "apache2",
	} {
		got, ok := UnitForProcess(in)
		assert.True(t, ok, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{"", "caddy (pid 5)", "httpd (pid 3)", "nginx", "haproxy (pid 1)", "nginx-x (pid 2)"} {
		_, ok := UnitForProcess(in)
		assert.False(t, ok, in)
	}
}

func TestTakeover_Success(t *testing.T) {
	h := newHarness(t)
	events := h.subscribe(t)
	res := h.e.Takeover(context.Background())
	require.Equal(t, OutcomeOK, res.Outcome, res.Message)

	assert.Equal(t, []string{
		"check " + TrialAddr,
		"stop nginx",
		"switch normal",
		"check " + NormalAddr,
		"disable nginx",
	}, h.c.list())
	assert.False(t, h.sd.isActive("nginx"))
	assert.False(t, h.sd.isEnabled("nginx"))
	assert.Equal(t, proxy.ListenNormal, h.px.LastMode())
	assert.Equal(t, []string{"203.0.113.0/24"}, h.px.LastCF())

	from, ok := h.setting(t, SettingMigratedFrom)
	require.True(t, ok)
	assert.Equal(t, "nginx", from)
	at, ok := h.setting(t, SettingMigratedAt)
	require.True(t, ok)
	assert.Equal(t, strconv.FormatInt(h.start.Unix(), 10), at)

	// Reconciliação pausada durante a troca e retomada com o status novo.
	assert.Equal(t, 1, h.dom.pauses)
	require.Len(t, h.dom.resumed, 1)
	assert.Equal(t, proxy.StateActive, h.dom.resumed[0].State)

	// Eventos na ordem, terminando com done/result.
	evs := events()
	require.NotEmpty(t, evs)
	var steps []string
	for _, ev := range evs[:len(evs)-1] {
		assert.Equal(t, OpTakeover, ev.Op)
		if ev.Status == StatusOK {
			steps = append(steps, ev.Step)
		}
		assert.False(t, ev.Done)
	}
	assert.Equal(t, []string{StepTrialCheck, StepStop, StepBind, StepVerify, StepDisable, StepRecord}, steps)
	last := evs[len(evs)-1]
	assert.True(t, last.Done)
	assert.Equal(t, OutcomeOK, last.Result)

	info, err := h.e.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "nginx", info.MigratedFrom)
	require.NotNil(t, info.MigratedAt)
	require.NotNil(t, info.Last)
	assert.Equal(t, OpTakeover, info.Last.Op)
	assert.Equal(t, OutcomeOK, info.Last.Outcome)
	assert.False(t, info.Running)
}

func TestTakeover_BindFailsRollsBack(t *testing.T) {
	h := newHarness(t)
	h.portsTaken = true // outro processo pegou a 443 depois do stop
	res := h.e.Takeover(context.Background())
	require.Equal(t, OutcomeRolledBack, res.Outcome, res.Message)
	assert.Contains(t, res.Message, "voltou para o nginx")
	assert.Contains(t, res.Message, "address already in use")

	log := h.c.list()
	// Tentou o bind por até 5 s (relógio falso), depois ensaio → start.
	normals := 0
	for _, l := range log {
		if l == "switch normal" {
			normals++
		}
	}
	assert.Greater(t, normals, 1)
	assert.GreaterOrEqual(t, h.clk.elapsed(h.start), BindRetry)
	assert.Less(t, h.clk.elapsed(h.start), BindRetry+5*time.Second)
	n := len(log)
	assert.Equal(t, []string{"switch trial", "start nginx"}, log[n-2:])
	assert.NotContains(t, log, "check "+NormalAddr)
	assert.NotContains(t, log, "disable nginx")

	assert.True(t, h.sd.isActive("nginx"))
	assert.Equal(t, proxy.ListenTrial, h.px.LastMode())
	_, ok := h.setting(t, SettingMigratedFrom)
	assert.False(t, ok)
	require.Len(t, h.dom.resumed, 1)
	assert.Equal(t, proxy.StateTrial, h.dom.resumed[0].State)
}

func TestTakeover_VerifyFailsRollsBack(t *testing.T) {
	h := newHarness(t)
	h.verify = func(host, addr string) SiteCheck {
		if addr == NormalAddr && host == "loja.exemplo.net" {
			return SiteCheck{Host: host, Message: "GET /: 502"}
		}
		return SiteCheck{Host: host, OK: true}
	}
	res := h.e.Takeover(context.Background())
	require.Equal(t, OutcomeRolledBack, res.Outcome, res.Message)
	assert.Contains(t, res.Message, "voltou para o nginx")
	assert.Contains(t, res.Message, "loja.exemplo.net")
	// Host com certificado próprio não espera: falha na primeira verificação.
	assert.Less(t, h.clk.elapsed(h.start), 2*time.Second)
	assert.Equal(t, []string{
		"check " + TrialAddr, "stop nginx", "switch normal", "check " + NormalAddr,
		"switch trial", "start nginx",
	}, h.c.list())
	assert.True(t, h.sd.isActive("nginx"))
	assert.True(t, h.sd.isEnabled("nginx"))
	_, ok := h.setting(t, SettingMigratedFrom)
	assert.False(t, ok)
	info, err := h.e.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, OutcomeRolledBack, info.Last.Outcome)
}

func TestTakeover_StopFailsRollsBack(t *testing.T) {
	h := newHarness(t)
	h.sd.StopErr = errors.New("Failed to stop nginx.service")
	res := h.e.Takeover(context.Background())
	require.Equal(t, OutcomeRolledBack, res.Outcome, res.Message)
	assert.Contains(t, res.Message, "Failed to stop nginx.service")
	assert.True(t, h.sd.isActive("nginx"))
	assert.NotContains(t, h.c.list(), "switch normal")
}

func TestTakeover_TrialCheckFailureChangesNothing(t *testing.T) {
	h := newHarness(t)
	h.verify = func(host, addr string) SiteCheck {
		if host == "exemplo.com" {
			return SiteCheck{Host: host, Message: "GET /: 503"}
		}
		return SiteCheck{Host: host, OK: true}
	}
	res := h.e.Takeover(context.Background())
	require.Equal(t, OutcomeFailed, res.Outcome)
	assert.Contains(t, res.Message, "exemplo.com")
	assert.Contains(t, res.Message, "nada foi alterado")
	assert.Equal(t, []string{"check " + TrialAddr}, h.c.list())
	assert.True(t, h.sd.isActive("nginx"))
}

func TestTakeover_RollbackStartFailsReportsFailed(t *testing.T) {
	h := newHarness(t)
	h.verify = func(host, addr string) SiteCheck {
		if addr == NormalAddr && host == "exemplo.com" {
			return SiteCheck{Host: host, Message: "GET /: 502"}
		}
		return SiteCheck{Host: host, OK: true}
	}
	h.sd.StartErr = errors.New("Job for nginx.service failed")
	res := h.e.Takeover(context.Background())
	require.Equal(t, OutcomeFailed, res.Outcome, res.Message)
	// Nunca os dois parados: o Caddy reassume 80/443 e a mensagem diz quem roda.
	assert.Contains(t, res.Message, "nginx não voltou")
	assert.Contains(t, res.Message, "Job for nginx.service failed")
	assert.Contains(t, res.Message, "Caddy voltou a servir 80/443")
	log := h.c.list()
	assert.Equal(t, "switch normal", log[len(log)-1])
	assert.Equal(t, proxy.ListenNormal, h.px.LastMode())
	// O "Voltar" fica disponível.
	from, ok := h.setting(t, SettingMigratedFrom)
	assert.True(t, ok)
	assert.Equal(t, "nginx", from)
}

func TestTakeover_NothingServingSaysSo(t *testing.T) {
	h := newHarness(t)
	h.portsTaken = true
	h.sd.StartNoop = true // start "funciona" mas a unit não fica ativa
	res := h.e.Takeover(context.Background())
	require.Equal(t, OutcomeFailed, res.Outcome)
	assert.Contains(t, res.Message, "nenhum servidor")
	assert.Contains(t, res.Message, "systemctl start nginx")
}

func TestTakeover_UnsupportedOwner409(t *testing.T) {
	h := newHarness(t)
	h.px.EnsureFn = func(context.Context) proxy.Status {
		return proxy.Status{State: proxy.StateTrial, Mode: proxy.ListenTrial, Process: "haproxy (pid 9)"}
	}
	err := h.e.StartTakeover(context.Background(), nil)
	var ref *Refusal
	require.ErrorAs(t, err, &ref)
	assert.Equal(t, CodeUnsupportedOwner, ref.Code)
	assert.Contains(t, ref.Message, "haproxy (pid 9)")
	assert.Empty(t, h.c.list())
	require.Len(t, h.dom.resumed, 1, "a pausa é desfeita")

	// nginx fora do systemd (unit inativa) também não é suportado.
	h.px.EnsureFn = func(context.Context) proxy.Status {
		return proxy.Status{State: proxy.StateTrial, Mode: proxy.ListenTrial, Process: "nginx (pid 9)"}
	}
	h.sd.active["nginx"] = false
	require.ErrorAs(t, h.e.StartTakeover(context.Background(), nil), &ref)
	assert.Equal(t, CodeUnsupportedOwner, ref.Code)

	h.sd.active["nginx"] = true
	h.sd.exists["nginx"] = false
	require.ErrorAs(t, h.e.StartTakeover(context.Background(), nil), &ref)
	assert.Equal(t, CodeUnsupportedOwner, ref.Code)
	assert.Empty(t, h.c.list())
	assert.False(t, h.e.Running())
}

func TestTakeover_NotTrial409(t *testing.T) {
	h := newHarness(t)
	h.px.EnsureFn = func(context.Context) proxy.Status {
		return proxy.Status{State: proxy.StateActive, Mode: proxy.ListenNormal}
	}
	err := h.e.StartTakeover(context.Background(), nil)
	var ref *Refusal
	require.ErrorAs(t, err, &ref)
	assert.Equal(t, CodeNotTrial, ref.Code)
	res := h.e.Takeover(context.Background())
	assert.Equal(t, OutcomeFailed, res.Outcome)
	assert.Equal(t, CodeNotTrial, res.Code)
	assert.Empty(t, h.c.list())
}

func TestTakeover_OneJobAtATime(t *testing.T) {
	h := newHarness(t)
	release := make(chan struct{})
	entered := make(chan struct{})
	base := h.e.Check
	h.e.Check = func(ctx context.Context, sites []proxy.Site, addr string, trial bool) []SiteCheck {
		if trial {
			close(entered)
			<-release
		}
		return base(ctx, sites, addr, trial)
	}
	done := make(chan Result, 1)
	require.NoError(t, h.e.StartTakeover(context.Background(), func(r Result) { done <- r }))
	<-entered
	assert.True(t, h.e.Running())
	var ref *Refusal
	require.ErrorAs(t, h.e.StartTakeover(context.Background(), nil), &ref)
	assert.Equal(t, CodeRunning, ref.Code)
	require.ErrorAs(t, h.e.StartRollback(context.Background(), nil), &ref)
	assert.Equal(t, CodeRunning, ref.Code)
	close(release)
	assert.Equal(t, OutcomeOK, (<-done).Outcome)
	assert.False(t, h.e.Running())
}

func TestTakeover_AutoCertWaitsUpTo60s(t *testing.T) {
	h := newHarness(t)
	h.dom.sites = []proxy.Site{site("exemplo.com", proxy.TLSAuto), site("loja.exemplo.net", proxy.TLSCustom)}
	var verifyStart time.Time
	h.verify = func(host, addr string) SiteCheck {
		if addr != NormalAddr {
			return SiteCheck{Host: host, OK: true, Pending: host == "exemplo.com"}
		}
		if verifyStart.IsZero() {
			verifyStart = h.clk.Now()
		}
		// O certificado automático sai depois de 40 s.
		if host == "exemplo.com" && h.clk.elapsed(verifyStart) < 40*time.Second {
			return SiteCheck{Host: host, Message: "handshake TLS: sem certificado"}
		}
		return SiteCheck{Host: host, OK: true}
	}
	res := h.e.Takeover(context.Background())
	require.Equal(t, OutcomeOK, res.Outcome, res.Message)
	assert.GreaterOrEqual(t, h.clk.elapsed(verifyStart), 40*time.Second)

	// Que nunca sai: desiste em 60 s e volta.
	h2 := newHarness(t)
	h2.dom.sites = []proxy.Site{site("exemplo.com", proxy.TLSAuto)}
	var start2 time.Time
	h2.verify = func(host, addr string) SiteCheck {
		if addr != NormalAddr {
			return SiteCheck{Host: host, OK: true, Pending: true}
		}
		if start2.IsZero() {
			start2 = h2.clk.Now()
		}
		return SiteCheck{Host: host, Message: "handshake TLS: sem certificado"}
	}
	res = h2.e.Takeover(context.Background())
	require.Equal(t, OutcomeRolledBack, res.Outcome, res.Message)
	el := h2.clk.elapsed(start2)
	assert.GreaterOrEqual(t, el, AutoCertWait)
	assert.Less(t, el, AutoCertWait+10*time.Second)
	assert.Contains(t, res.Message, "exemplo.com")
	assert.True(t, h2.sd.isActive("nginx"))
}

func TestRollback_EnablesAndStartsUnit(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, OutcomeOK, h.e.Takeover(context.Background()).Outcome)
	h.c.log = nil
	events := h.subscribe(t)

	res := h.e.Rollback(context.Background())
	require.Equal(t, OutcomeOK, res.Outcome, res.Message)
	assert.Equal(t, []string{"switch trial", "enable nginx", "start nginx"}, h.c.list())
	assert.True(t, h.sd.isActive("nginx"))
	assert.True(t, h.sd.isEnabled("nginx"))
	assert.Equal(t, proxy.ListenTrial, h.px.LastMode())
	_, ok := h.setting(t, SettingMigratedFrom)
	assert.False(t, ok)
	_, ok = h.setting(t, SettingMigratedAt)
	assert.False(t, ok)
	evs := events()
	require.NotEmpty(t, evs)
	last := evs[len(evs)-1]
	assert.True(t, last.Done)
	assert.Equal(t, OpRollback, last.Op)
	assert.Equal(t, OutcomeOK, last.Result)
	info, err := h.e.Info(context.Background())
	require.NoError(t, err)
	assert.Empty(t, info.MigratedFrom)
	assert.Equal(t, OpRollback, info.Last.Op)

	// Sem migração registrada: indisponível.
	var ref *Refusal
	require.ErrorAs(t, h.e.StartRollback(context.Background(), nil), &ref)
	assert.Equal(t, CodeNotMigrated, ref.Code)
}

func TestRollback_UnitFileGoneUnavailable(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, OutcomeOK, h.e.Takeover(context.Background()).Outcome)
	h.sd.exists["nginx"] = false
	var ref *Refusal
	require.ErrorAs(t, h.e.StartRollback(context.Background(), nil), &ref)
	assert.Equal(t, CodeNotMigrated, ref.Code)
	info, err := h.e.Info(context.Background())
	require.NoError(t, err)
	assert.False(t, info.RollbackAvailable)
	// Unit gravada fora da lista suportada também é recusada.
	require.NoError(t, h.db.SetSetting(context.Background(), SettingMigratedFrom, "sshd"))
	require.ErrorAs(t, h.e.StartRollback(context.Background(), nil), &ref)
	assert.Equal(t, CodeNotMigrated, ref.Code)
}

func TestRollback_StartFailsCaddyRetakes(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, OutcomeOK, h.e.Takeover(context.Background()).Outcome)
	h.c.log = nil
	h.sd.StartErr = errors.New("Job for nginx.service failed")
	res := h.e.Rollback(context.Background())
	require.Equal(t, OutcomeFailed, res.Outcome)
	assert.Contains(t, res.Message, "Caddy voltou a servir 80/443")
	assert.Equal(t, []string{"switch trial", "enable nginx", "start nginx", "switch normal", "disable nginx"}, h.c.list())
	assert.Equal(t, proxy.ListenNormal, h.px.LastMode())
	assert.False(t, h.sd.isEnabled("nginx"))
	from, ok := h.setting(t, SettingMigratedFrom)
	assert.True(t, ok)
	assert.Equal(t, "nginx", from)
}

func TestRollback_TrialSwitchFailsChangesNothing(t *testing.T) {
	h := newHarness(t)
	require.Equal(t, OutcomeOK, h.e.Takeover(context.Background()).Outcome)
	h.c.log = nil
	h.px.SwitchFn = func(context.Context, []proxy.Site, proxy.Listen, []string) error {
		h.c.add("switch")
		return errors.New("admin api fora do ar")
	}
	res := h.e.Rollback(context.Background())
	require.Equal(t, OutcomeFailed, res.Outcome)
	assert.Contains(t, res.Message, "nada mudou")
	assert.Equal(t, []string{"switch"}, h.c.list())
	assert.False(t, h.sd.isActive("nginx"))
}

func TestTrialCheck_CachedFor10s(t *testing.T) {
	h := newHarness(t)
	n := 0
	h.e.Check = func(_ context.Context, sites []proxy.Site, addr string, trial bool) []SiteCheck {
		n++
		assert.Equal(t, TrialAddr, addr)
		assert.True(t, trial)
		return []SiteCheck{{Host: "exemplo.com", OK: true}}
	}
	_, err := h.e.TrialCheck(context.Background())
	require.NoError(t, err)
	_, err = h.e.TrialCheck(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	_ = h.clk.Sleep(context.Background(), 11*time.Second)
	_, err = h.e.TrialCheck(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	h.dom.status = proxy.Status{State: proxy.StateActive, Mode: proxy.ListenNormal}
	var ref *Refusal
	_, err = h.e.TrialCheck(context.Background())
	require.ErrorAs(t, err, &ref)
	assert.Equal(t, CodeNotTrial, ref.Code)
}
