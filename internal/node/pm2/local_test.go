package pm2

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// god é a linha de comando do God Daemon do PM2 em /proc/<pid>/cmdline.
const god = "PM2 v5.3.0: God Daemon (/home/deploy/.pm2)"

// fakeProc cria um /proc falso com <pid>/cmdline.
func fakeProc(t *testing.T, pid int, cmdline string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, strconv.Itoa(pid))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o644))
	return root
}

// writePid grava o pid do processo de teste em dir/pm2.pid.
func writePid(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pm2.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600))
}

// withGod troca o /proc do leitor por um falso em que o pid do processo de
// teste é o God Daemon do PM2.
func withGod(t *testing.T, l *Local) *Local {
	t.Helper()
	l.procRoot = fakeProc(t, os.Getpid(), god)
	return l
}

func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("depende de scripts shell e pid files Unix")
	}
}

// fakeBin cria um "pm2" falso que registra cada execução (com o PM2_HOME
// recebido) em runs.log e imprime o testdata/jlist.json.
func fakeBin(t *testing.T) (bin, runs string) {
	t.Helper()
	dir := t.TempDir()
	runs = filepath.Join(dir, "runs.log")
	data, err := filepath.Abs("testdata/jlist.json")
	require.NoError(t, err)
	bin = filepath.Join(dir, "pm2")
	script := "#!/bin/sh\necho \"run $PM2_HOME\" >> '" + runs + "'\ncat '" + data + "'\n"
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755))
	return bin, runs
}

func countRuns(t *testing.T, runs string) int {
	t.Helper()
	b, err := os.ReadFile(runs)
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}

func TestLocal_NoBinary(t *testing.T) {
	l := NewLocal(filepath.Join(t.TempDir(), "nao-existe", "pm2"), "")
	s, err := l.Snapshot(context.Background())
	require.NoError(t, err)
	assert.False(t, s.Available)
	assert.False(t, s.DaemonRunning)
	assert.NotNil(t, s.Processes)
	assert.Empty(t, s.Processes)
}

func TestLocal_NoDaemonDoesNotExec(t *testing.T) {
	skipWindows(t)
	bin, runs := fakeBin(t)
	t.Setenv("PM2_HOME", t.TempDir())
	s, err := NewLocal(bin, "").Snapshot(context.Background())
	require.NoError(t, err)
	assert.True(t, s.Available)
	assert.False(t, s.DaemonRunning)
	assert.Empty(t, s.Processes)
	assert.Equal(t, 0, countRuns(t, runs))
}

func TestLocal_StalePidDoesNotExec(t *testing.T) {
	skipWindows(t)
	bin, runs := fakeBin(t)
	home := t.TempDir()
	t.Setenv("PM2_HOME", home)
	// pid improvável de existir.
	require.NoError(t, os.WriteFile(filepath.Join(home, "pm2.pid"), []byte("2147483646\n"), 0o600))
	s, err := NewLocal(bin, "").Snapshot(context.Background())
	require.NoError(t, err)
	assert.True(t, s.Available)
	assert.False(t, s.DaemonRunning)
	assert.Equal(t, 0, countRuns(t, runs))
}

func TestLocal_ReusedPidDoesNotExec(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("a conferência por /proc/<pid>/cmdline é só no Linux")
	}
	bin, runs := fakeBin(t)
	home := t.TempDir()
	// O pid existe (é o próprio teste), mas não é o God Daemon do PM2: foi
	// reaproveitado depois de um reboot.
	writePid(t, home)
	l := NewLocal(bin, home)
	l.procRoot = fakeProc(t, os.Getpid(), "/usr/sbin/sshd\x00-D\x00")
	s, err := l.Snapshot(context.Background())
	require.NoError(t, err)
	assert.True(t, s.Available)
	assert.False(t, s.DaemonRunning)
	assert.Equal(t, 0, countRuns(t, runs))

	// O /proc de verdade: o processo de teste também não é o PM2.
	s, err = NewLocal(bin, home).Snapshot(context.Background())
	require.NoError(t, err)
	assert.False(t, s.DaemonRunning)
	assert.Equal(t, 0, countRuns(t, runs))
}

func TestLocal_HomeOverride(t *testing.T) {
	skipWindows(t)
	bin, runs := fakeBin(t)
	home := t.TempDir()
	writePid(t, home)
	// O PM2_HOME do ambiente aponta para outro lugar, sem pid file.
	t.Setenv("PM2_HOME", t.TempDir())
	s, err := withGod(t, NewLocal(bin, home)).Snapshot(context.Background())
	require.NoError(t, err)
	assert.True(t, s.DaemonRunning)
	require.Equal(t, 1, countRuns(t, runs))
	// O pm2 roda com o PM2_HOME configurado.
	b, err := os.ReadFile(runs)
	require.NoError(t, err)
	assert.Equal(t, "run "+home+"\n", string(b))
}

func TestLocal_RunsWithDaemon(t *testing.T) {
	skipWindows(t)
	bin, runs := fakeBin(t)
	home := t.TempDir()
	t.Setenv("PM2_HOME", home)
	writePid(t, home)
	s, err := withGod(t, NewLocal(bin, "")).Snapshot(context.Background())
	require.NoError(t, err)
	assert.True(t, s.Available)
	assert.True(t, s.DaemonRunning)
	require.Len(t, s.Processes, 2)
	assert.Equal(t, "api-exemplo", s.Processes[0].Name)
	assert.Equal(t, 1, countRuns(t, runs))
}

func TestLocal_CachesFor10s(t *testing.T) {
	skipWindows(t)
	bin, runs := fakeBin(t)
	home := t.TempDir()
	writePid(t, home)

	now := time.Unix(1_000_000, 0)
	l := withGod(t, NewLocal(bin, home))
	l.now = func() time.Time { return now }

	_, err := l.Snapshot(context.Background())
	require.NoError(t, err)
	now = now.Add(5 * time.Second)
	_, err = l.Snapshot(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, countRuns(t, runs))

	now = now.Add(6 * time.Second) // 11 s no total
	_, err = l.Snapshot(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, countRuns(t, runs))
}
