package migrate

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/proxy"
)

// portsFree põe o harness em ensaio com 80/443 livres (nginx parado).
func portsFree(t *testing.T, h *harness, trialUnit string) {
	t.Helper()
	h.sd.active["nginx"] = false
	h.dom.status = proxy.Status{State: proxy.StateTrial, Mode: proxy.ListenTrial, Message: proxy.PortsFreeMessage}
	if trialUnit != "" {
		require.NoError(t, h.db.SetSetting(context.Background(), proxy.SettingTrialUnit, trialUnit))
	}
}

func TestTakeover_PortsFreeUsesTrialUnit(t *testing.T) {
	h := newHarness(t)
	portsFree(t, h, "nginx")
	require.NoError(t, h.e.PrecheckTakeover(context.Background()))

	res := h.e.Takeover(context.Background())
	require.Equal(t, OutcomeOK, res.Outcome, res.Message)
	assert.Equal(t, []string{
		"check " + TrialAddr,
		"switch normal",
		"check " + NormalAddr,
		"disable nginx",
	}, h.c.list(), "sem stop: as portas já estavam livres")
	from, ok := h.setting(t, SettingMigratedFrom)
	require.True(t, ok)
	assert.Equal(t, "nginx", from)
	info, err := h.e.Info(context.Background())
	require.NoError(t, err)
	assert.True(t, info.RollbackAvailable)
}

func TestTakeover_PortsFreeWithoutUnitRecordsNothing(t *testing.T) {
	h := newHarness(t)
	portsFree(t, h, "")
	require.NoError(t, h.e.PrecheckTakeover(context.Background()))

	res := h.e.Takeover(context.Background())
	require.Equal(t, OutcomeOK, res.Outcome, res.Message)
	assert.Equal(t, []string{"check " + TrialAddr, "switch normal", "check " + NormalAddr}, h.c.list())
	_, ok := h.setting(t, SettingMigratedFrom)
	assert.False(t, ok, "sem unit conhecida, sem Voltar")
	assert.Contains(t, res.Message, "O Caddy assumiu as portas 80/443")
}

func TestTakeover_PortsFreeVerifyFailsRollsBackToUnit(t *testing.T) {
	h := newHarness(t)
	portsFree(t, h, "nginx")
	h.verify = func(host, addr string) SiteCheck {
		if addr == NormalAddr {
			return SiteCheck{Host: host, Message: "502"}
		}
		return SiteCheck{Host: host, OK: true}
	}
	res := h.e.Takeover(context.Background())
	assert.Equal(t, OutcomeRolledBack, res.Outcome, res.Message)
	assert.Equal(t, []string{
		"check " + TrialAddr,
		"switch normal",
		"check " + NormalAddr,
		"switch trial",
		"start nginx",
	}, h.c.list())
	assert.True(t, h.sd.isActive("nginx"))
	_, ok := h.setting(t, SettingMigratedFrom)
	assert.False(t, ok)
}

func TestTakeover_PortsFreeVerifyFailsWithoutUnit(t *testing.T) {
	h := newHarness(t)
	portsFree(t, h, "")
	h.verify = func(host, addr string) SiteCheck {
		if addr == NormalAddr {
			return SiteCheck{Host: host, Message: "502"}
		}
		return SiteCheck{Host: host, OK: true}
	}
	res := h.e.Takeover(context.Background())
	assert.Equal(t, OutcomeRolledBack, res.Outcome, res.Message)
	assert.Equal(t, []string{"check " + TrialAddr, "switch normal", "check " + NormalAddr, "switch trial"}, h.c.list())
}

func TestPrecheck_UnsupportedOwnerOnlyWhenPortsHeld(t *testing.T) {
	h := newHarness(t)
	h.dom.status = proxy.Status{State: proxy.StateTrial, Mode: proxy.ListenTrial, Process: "caddy (pid 5)"}
	var ref *Refusal
	require.True(t, errors.As(h.e.PrecheckTakeover(context.Background()), &ref))
	assert.Equal(t, CodeUnsupportedOwner, ref.Code)

	portsFree(t, h, "")
	assert.NoError(t, h.e.PrecheckTakeover(context.Background()))
}

func TestRecoverInterrupted_TrialWithUnitStoppedStartsIt(t *testing.T) {
	// O KoreGO caiu entre o stop do nginx e o bind: o Caddy segue em ensaio
	// (modo gravado), então o nginx volta para os sites não ficarem fora.
	h := newHarness(t)
	setMarker(t, h, OpTakeover)
	h.sd.active["nginx"] = false
	h.dom.reconcile = func(context.Context) proxy.Status {
		return proxy.Status{State: proxy.StateTrial, Mode: proxy.ListenTrial, Message: proxy.PortsFreeMessage}
	}
	require.NoError(t, h.e.RecoverInterrupted(context.Background()))
	assert.True(t, h.sd.isActive("nginx"))
	assert.Equal(t, []string{"start nginx"}, h.c.list())
	info, err := h.e.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, OutcomeFailed, info.Last.Outcome)
	assert.Contains(t, info.Last.Message, "O nginx está ativo")
}
