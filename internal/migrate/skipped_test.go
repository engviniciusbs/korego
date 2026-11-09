package migrate

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/proxy"
)

func TestTakeover_DomainsSkipped409(t *testing.T) {
	h := newHarness(t)
	h.dom.skipped = []proxy.SkippedSite{
		{Host: "nova.exemplo.com", Reason: "rota / aponta para uma app sem porta (sem deploy)"},
		{Host: "www.exemplo.com", Reason: "o host www.exemplo.com já é usado pelo domínio exemplo.com"},
	}
	var ref *Refusal
	require.True(t, errors.As(h.e.PrecheckTakeover(context.Background()), &ref))
	assert.Equal(t, CodeDomainsSkipped, ref.Code)
	assert.Contains(t, ref.Message, "nova.exemplo.com (rota / aponta para uma app sem porta (sem deploy))")
	assert.Contains(t, ref.Message, "www.exemplo.com (o host www.exemplo.com já é usado pelo domínio exemplo.com)")

	err := h.e.StartTakeover(context.Background(), func(Result) { t.Error("não deveria rodar") })
	require.True(t, errors.As(err, &ref))
	assert.Equal(t, CodeDomainsSkipped, ref.Code)
	assert.Empty(t, h.c.list(), "nada foi tocado")
	assert.False(t, h.dom.paused)
	assert.False(t, h.e.Running())
	_, ok := h.setting(t, SettingInProgress)
	assert.False(t, ok)
}
