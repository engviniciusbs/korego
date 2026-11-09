package proxy

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/docker/dockertest"
)

// Versões antigas carregavam a config com persist ligado: o Caddy gravou as
// chaves (load_pem) em /config/caddy/autosave.json, no volume. Ensure pede ao
// container para apagar o arquivo, uma vez por container.
func TestEnsure_ScrubsAutosaveOncePerContainer(t *testing.T) {
	_, admin := newFakeAdmin(t)
	var cmds [][]string
	var ids []string
	d := &dockertest.Fake{
		ListFn: runningOwn(specLabels),
		ExecFn: func(_ context.Context, id string, cmd []string) (int, error) {
			ids = append(ids, id)
			cmds = append(cmds, cmd)
			return 0, nil
		},
	}
	c := newCaddy(d, admin)
	c.PortOwner = freePorts
	c.Ensure(context.Background())
	c.Ensure(context.Background())
	require.Len(t, cmds, 1, "uma vez por container")
	assert.Equal(t, []string{"c1"}, ids)
	assert.Contains(t, strings.Join(cmds[0], " "), "rm -f /config/caddy/autosave.json")
}

func TestEnsure_AutosaveScrubFailureDoesNotBreakEnsure(t *testing.T) {
	_, admin := newFakeAdmin(t)
	calls := 0
	d := &dockertest.Fake{
		ListFn: runningOwn(specLabels),
		ExecFn: func(context.Context, string, []string) (int, error) { calls++; return 0, assert.AnError },
	}
	c := newCaddy(d, admin)
	c.PortOwner = freePorts
	assert.Equal(t, StateActive, c.Ensure(context.Background()).State)
	c.Ensure(context.Background())
	assert.Equal(t, 1, calls, "sem repetir a cada volta")
}
