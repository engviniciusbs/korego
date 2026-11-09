//go:build integration && linux

package usage

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/procs"
)

const itProject = "consumo-it"

// runSleepContainer sobe um busybox "sleep 300" com o label de projeto compose
// e garante a remoção ao fim do teste.
func runSleepContainer(t *testing.T) string {
	t.Helper()
	if err := exec.Command("docker", "image", "inspect", "busybox").Run(); err != nil {
		out, err := exec.Command("docker", "pull", "busybox").CombinedOutput()
		require.NoError(t, err, string(out))
	}
	out, err := exec.Command("docker", "run", "-d",
		"--label", "com.docker.compose.project="+itProject,
		"busybox", "sleep", "300").CombinedOutput()
	require.NoError(t, err, string(out))
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	return id
}

// containerCgroup devolve o /proc/<pid>/cgroup do processo principal do
// container, para diagnosticar uma falha de atribuição.
func containerCgroup(id string) string {
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Pid}}", id).Output()
	if err != nil {
		return fmt.Sprintf("docker inspect: %v", err)
	}
	pid := strings.TrimSpace(string(out))
	b, err := os.ReadFile("/proc/" + pid + "/cgroup")
	if err != nil {
		return fmt.Sprintf("pid %s: %v", pid, err)
	}
	return fmt.Sprintf("pid %s: %s", pid, strings.TrimSpace(string(b)))
}

func hasSleep(o Owner) bool {
	for _, p := range o.Processes {
		if p.Name == "sleep" || strings.HasPrefix(p.Cmd, "sleep ") {
			return true
		}
	}
	return false
}

// Com procs.NewLocal e o Docker reais, o processo do container aparece no dono
// "project:consumo-it" em poucos segundos.
func TestCollector_RealDockerProjectOwner(t *testing.T) {
	dc := docker.NewLocal()
	require.NoError(t, dc.Ping(context.Background()), "Docker indisponível")

	id := runSleepContainer(t)

	ctx, cancel := context.WithCancel(context.Background())
	c := &Collector{Procs: procs.NewLocal(), Docker: dc, Interval: 500 * time.Millisecond}
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()
	// Registrado depois do container: para o coletor antes de remover o container.
	t.Cleanup(func() {
		cancel()
		<-done
	})

	const key = "project:" + itProject
	deadline := time.Now().Add(15 * time.Second)
	var last Snapshot
	for time.Now().Before(deadline) {
		if snap, ok := c.Latest(); ok {
			last = snap
			for _, o := range snap.Owners {
				if o.Key == key && hasSleep(o) {
					require.Equal(t, KindContainer, o.Kind)
					require.Equal(t, itProject, o.Label)
					require.NotNil(t, o.Managed)
					require.False(t, *o.Managed, "container sem label korego.managed é externo")
					require.True(t, snap.DockerOK)
					return
				}
			}
		}
		time.Sleep(250 * time.Millisecond)
	}

	keys := make([]string, 0, len(last.Owners))
	for _, o := range last.Owners {
		keys = append(keys, fmt.Sprintf("%s(%d)", o.Key, o.ProcCount))
	}
	t.Fatalf("dono %q com o processo sleep não apareceu em 15 s; dockerOK=%v donos=%v cgroup=%s",
		key, last.DockerOK, keys, containerCgroup(id))
}
