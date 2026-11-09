//go:build integration

package docker

import (
	"context"
	"net"
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

func TestLocal_StatsStoppedContainerEnds(t *testing.T) {
	id := runBusybox(t, "sleep", "300")
	out, err := exec.Command("docker", "stop", "-t", "0", id).CombinedOutput()
	require.NoError(t, err, string(out))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var n int
	// Parado, o stream termina sozinho (nil), sem amostras, antes do timeout.
	err = NewLocal().Stats(ctx, id, func(Stats) { n++ })
	require.NoError(t, err)
	assert.NoError(t, ctx.Err())
	assert.Equal(t, 0, n)
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

// freePort devolve uma porta TCP livre em 127.0.0.1.
func freePort(t *testing.T) uint16 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	return uint16(ln.Addr().(*net.TCPAddr).Port)
}

func TestLocal_RunPullImages(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	l := NewLocal()

	var lines []string
	require.NoError(t, l.Pull(ctx, "busybox:latest", func(s string) { lines = append(lines, s) }))
	assert.NotEmpty(t, lines)

	imgs, err := l.Images(ctx, "busybox")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(imgs), 1)
	assert.Equal(t, "busybox:latest", imgs[0].Ref)
	assert.NotEmpty(t, imgs[0].ID)
	assert.False(t, imgs[0].CreatedAt.IsZero())

	label := "korego.test.run" + strings.ReplaceAll(t.Name(), "/", "_")
	port := freePort(t)
	id, err := l.Run(ctx, RunSpec{
		Name:   "korego-test-run-pull",
		Image:  "busybox:latest",
		Labels: map[string]string{label: "1"},
		Env:    []string{"SEGREDO=valor"},
		Ports:  []PortMapping{{HostIP: "127.0.0.1", HostPort: port, ContainerPort: 8080}},
		Cmd:    []string{"sleep", "300"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.RemoveForce(context.Background(), id) })

	got, err := l.ListByLabel(ctx, label, "1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, id, got[0].ID)
	assert.Equal(t, "running", got[0].State)

	d, err := l.Inspect(ctx, id)
	require.NoError(t, err)
	assert.Contains(t, d.Ports, Port{IP: "127.0.0.1", PrivatePort: 8080, PublicPort: port, Proto: "tcp"})

	require.NoError(t, l.RemoveForce(ctx, id))
	got, err = l.ListByLabel(ctx, label, "1")
	require.NoError(t, err)
	assert.Empty(t, got)
	// Remover de novo é tolerado.
	assert.NoError(t, l.RemoveForce(ctx, id))
}

func TestLocal_RunNameConflict(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	l := NewLocal()
	require.NoError(t, l.Pull(ctx, "busybox:latest", func(string) {}))

	spec := RunSpec{Name: "korego-test-conflito", Image: "busybox:latest", Cmd: []string{"sleep", "300"}}
	id, err := l.Run(ctx, spec)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.RemoveForce(context.Background(), id) })

	_, err = l.Run(ctx, spec)
	assert.ErrorIs(t, err, ErrConflict)
}

func TestLocal_PullError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err := NewLocal().Pull(ctx, "korego-inexistente.invalid/x/y:1", func(string) {})
	assert.Error(t, err)
}

func TestLocal_Exec(t *testing.T) {
	id := runBusybox(t, "sleep", "300")
	l := NewLocal()
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	code, err := l.Exec(ctx, id, []string{"sh", "-c", "touch /tmp/x && [ -e /tmp/x ] || exit 3; rm -f /tmp/x"})
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	code, err = l.Exec(ctx, id, []string{"sh", "-c", "[ -e /tmp/x ] || exit 3; rm -f /tmp/x"})
	require.NoError(t, err)
	assert.Equal(t, 3, code)
}
