package deploy

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/engviniciusbs/korego/internal/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeployLog_NumberedAndRead(t *testing.T) {
	dir := t.TempDir()
	hub := events.NewHub()
	ch, cancel := hub.Subscribe("deploy-logs:7")
	defer cancel()

	l, err := OpenDeployLog(dir, 7, hub)
	require.NoError(t, err)
	require.NoError(t, l.Line("primeira"))
	require.NoError(t, l.Line("segunda"))
	require.NoError(t, l.Close())

	// reabrir continua a numeração (append)
	l2, err := OpenDeployLog(dir, 7, hub)
	require.NoError(t, err)
	require.NoError(t, l2.Line("terceira"))
	require.NoError(t, l2.Close())

	raw, err := os.ReadFile(filepath.Join(dir, "logs", "deploy-7.log"))
	require.NoError(t, err)
	assert.Equal(t, "primeira\nsegunda\nterceira\n", string(raw))

	all, err := ReadLog(dir, 7, 0)
	require.NoError(t, err)
	assert.Equal(t, []LogEntry{{1, "primeira"}, {2, "segunda"}, {3, "terceira"}}, all)

	part, err := ReadLog(dir, 7, 1)
	require.NoError(t, err)
	assert.Equal(t, []LogEntry{{2, "segunda"}, {3, "terceira"}}, part)

	none, err := ReadLog(dir, 7, 3)
	require.NoError(t, err)
	assert.Empty(t, none)

	missing, err := ReadLog(dir, 99, 0)
	require.NoError(t, err)
	assert.Empty(t, missing)

	var got []LogEntry
	for len(got) < 3 {
		select {
		case ev := <-ch:
			got = append(got, ev.Data.(LogEntry))
		case <-time.After(time.Second):
			t.Fatalf("eventos recebidos: %v", got)
		}
	}
	assert.Equal(t, []LogEntry{{1, "primeira"}, {2, "segunda"}, {3, "terceira"}}, got)

	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(dir, "logs"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), st.Mode().Perm())
	}
}

func TestLineSplitter_PartialAndCR(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenDeployLog(dir, 1, nil)
	require.NoError(t, err)
	s := &lineSplitter{dst: l}
	_, _ = s.Write([]byte("abc"))
	_, _ = s.Write([]byte("def\r\nlinha 2\nresto"))
	s.Flush()
	s.Flush() // sem nada pendente, não grava linha vazia
	require.NoError(t, l.Close())

	all, err := ReadLog(dir, 1, 0)
	require.NoError(t, err)
	assert.Equal(t, []LogEntry{{1, "abcdef"}, {2, "linha 2"}, {3, "resto"}}, all)
}
