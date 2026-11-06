//go:build integration

package docker

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runBusybox cria um container busybox via CLI do Docker e garante a remoção ao fim do teste.
func runBusybox(t *testing.T, args ...string) string {
	t.Helper()
	if err := exec.Command("docker", "image", "inspect", "busybox").Run(); err != nil {
		out, err := exec.Command("docker", "pull", "busybox").CombinedOutput()
		require.NoError(t, err, string(out))
	}
	full := append([]string{"run", "-d", "--label", "korego.managed=true", "busybox"}, args...)
	out, err := exec.Command("docker", full...).CombinedOutput()
	require.NoError(t, err, string(out))
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	return id
}

func TestLocal_Lifecycle(t *testing.T) {
	ctx := context.Background()
	l := NewLocal()
	require.NoError(t, l.Ping(ctx))

	id := runBusybox(t, "sleep", "300")

	list, err := l.List(ctx)
	require.NoError(t, err)
	var found *Container
	for i := range list {
		if list[i].ID == id {
			found = &list[i]
		}
	}
	require.NotNil(t, found, "container criado deve aparecer na lista")
	assert.True(t, found.Managed)
	assert.Equal(t, "running", found.State)
	assert.NotEmpty(t, found.Name)

	d, err := l.Inspect(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "running", d.State)
	assert.Equal(t, "sleep 300", d.Command)

	// Remover rodando, sem force → conflito.
	assert.ErrorIs(t, l.Remove(ctx, id), ErrConflict)

	require.NoError(t, l.Stop(ctx, id))
	d, err = l.Inspect(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "exited", d.State)

	require.NoError(t, l.Remove(ctx, id))
	_, err = l.Inspect(ctx, id)
	assert.ErrorIs(t, err, ErrNotFound)
	assert.ErrorIs(t, l.Start(ctx, id), ErrNotFound)
}

func TestLocal_LogsTailAndFollow(t *testing.T) {
	ctx := context.Background()
	l := NewLocal()
	id := runBusybox(t, "sh", "-c", "seq 1 1000; sleep 5; echo fim; sleep 1; echo erro >&2; sleep 300")

	// Espera as 1000 linhas estarem no log.
	require.Eventually(t, func() bool {
		n := 0
		_ = l.Logs(ctx, id, LogOptions{}, func(LogLine) { n++ })
		return n >= 1000
	}, 15*time.Second, 100*time.Millisecond)

	var got []LogLine
	require.NoError(t, l.Logs(ctx, id, LogOptions{Tail: 500}, func(ln LogLine) { got = append(got, ln) }))
	require.Len(t, got, 500)
	assert.Equal(t, "501", got[0].Text)
	assert.Equal(t, "1000", got[len(got)-1].Text)
	for i, ln := range got {
		assert.Equal(t, "stdout", ln.Stream)
		assert.Len(t, ln.TS, 19)
		if i > 0 {
			assert.Less(t, got[i-1].TS, ln.TS)
		}
	}

	// Follow a partir da última linha vista: não repete "1000", recebe
	// "fim" (stdout) e depois "erro" (stderr).
	fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var followed []LogLine
	err := l.Logs(fctx, id, LogOptions{Since: got[len(got)-1].TS, Follow: true}, func(ln LogLine) {
		followed = append(followed, LogLine{Stream: ln.Stream, Text: ln.Text})
		if ln.Text == "erro" {
			cancel()
		}
	})
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, []LogLine{{Stream: "stdout", Text: "fim"}, {Stream: "stderr", Text: "erro"}}, followed)
}

func TestLocal_StatsAndEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	l := NewLocal()

	evs := make(chan Event, 16)
	go func() { _ = l.Events(ctx, func(e Event) { evs <- e }) }()
	time.Sleep(500 * time.Millisecond)

	id := runBusybox(t, "sleep", "300")
	deadline := time.After(10 * time.Second)
	var started bool
	for !started {
		select {
		case e := <-evs:
			if e.ID == id && e.Action == "start" {
				started = true
				assert.NotEmpty(t, e.Name)
			}
		case <-deadline:
			t.Fatal("evento start não chegou")
		}
	}

	sctx, scancel := context.WithTimeout(ctx, 10*time.Second)
	defer scancel()
	var n int
	err := l.Stats(sctx, id, func(s Stats) {
		n++
		assert.Positive(t, s.MemLimitBytes)
		assert.GreaterOrEqual(t, s.CPUPercent, 0.0)
		if n == 2 {
			scancel()
		}
	})
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 2, n)
}

func TestLocal_UnavailableSocket(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///nao/existe")
	l := NewLocal()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	assert.ErrorIs(t, l.Ping(ctx), ErrUnavailable)
	_, err := l.List(ctx)
	assert.ErrorIs(t, err, ErrUnavailable)
}
