//go:build linux

package procs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// copyFixture copia testdata/ para um diretório temporário editável.
func copyFixture(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	require.NoError(t, os.CopyFS(dst, os.DirFS("testdata")))
	return dst
}

func write(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func byPID(ps []Proc, pid int) *Proc {
	for i := range ps {
		if ps[i].PID == pid {
			return &ps[i]
		}
	}
	return nil
}

func TestLocal_CPUDeltaAndReuse(t *testing.T) {
	dir := copyFixture(t)
	l := newLocalAt(filepath.Join(dir, "proc"), filepath.Join(dir, "etc/passwd"))
	ctx := context.Background()

	ps, err := l.Snapshot(ctx)
	require.NoError(t, err)
	require.Len(t, ps, 3)
	for _, p := range ps {
		assert.Zero(t, p.CPUPercent, "primeira leitura vale 0")
	}
	n := byPID(ps, 100)
	require.NotNil(t, n)
	assert.Equal(t, "nginx", n.Name)
	assert.Equal(t, []string{"nginx", "-g", "daemon off;"}, n.Cmdline)
	assert.Equal(t, 33, n.UID)
	assert.Equal(t, "www-data", n.User)
	assert.EqualValues(t, 1234*1024, n.RSSBytes)
	assert.EqualValues(t, 500, n.StartTicks)
	assert.Contains(t, n.Cgroup, "docker-")
	assert.Equal(t, 1, n.PPID)
	assert.Equal(t, "", byPID(ps, 200).User, "uid sem entrada no passwd")
	assert.Equal(t, "my (weird) proc", byPID(ps, 200).Name)

	// 2ª leitura: total 1000 -> 1200; pid 100 de 300 -> 350 ticks (Δ50 => 25%).
	// pid 200 reaproveitado (novo starttime) com muitos ticks => 0%.
	write(t, filepath.Join(dir, "proc/stat"), "cpu  150 0 150 900 0 0 0 0 0 0\n")
	write(t, filepath.Join(dir, "proc/100/stat"), "100 (nginx) S 1 100 100 0 -1 4194560 0 0 0 0 230 120 0 0 20 0 1 0 500 1000 1000\n")
	write(t, filepath.Join(dir, "proc/200/stat"), "200 (other) R 100 200 200 0 -1 4194560 0 0 0 0 5000 5000 0 0 20 0 1 0 901 1000 1000\n")
	ps, err = l.Snapshot(ctx)
	require.NoError(t, err)
	assert.InDelta(t, 25.0, byPID(ps, 100).CPUPercent, 0.001)
	assert.Zero(t, byPID(ps, 200).CPUPercent, "PID reaproveitado")
	assert.Zero(t, byPID(ps, 1).CPUPercent)
}

func TestLocal_VanishedProcessSkipped(t *testing.T) {
	dir := copyFixture(t)
	require.NoError(t, os.Remove(filepath.Join(dir, "proc/200/stat")))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "proc/300"), 0o755)) // sem stat
	l := newLocalAt(filepath.Join(dir, "proc"), filepath.Join(dir, "etc/passwd"))
	ps, err := l.Snapshot(context.Background())
	require.NoError(t, err)
	assert.Len(t, ps, 2)
	assert.Nil(t, byPID(ps, 200))
}
