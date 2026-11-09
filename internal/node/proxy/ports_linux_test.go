//go:build linux

package proxy

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tcpHeader = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestPortOwner_ProcFixture(t *testing.T) {
	root := t.TempDir()
	// :0050 (80) em LISTEN, inode 4242; :01BB (443) só ESTABLISHED (01), inode 77.
	writeFixture(t, filepath.Join(root, "net", "tcp"), tcpHeader+
		"   0: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 4242 1 0000000000000000 100 0 0 10 0\n"+
		"   1: 0100007F:01BB 0100007F:9C40 01 00000000:00000000 00:00000000 00000000     0        0 77 1 0000000000000000 100 0 0 10 0\n")
	// :1F90 (8080) em LISTEN no ::, inode 5151.
	writeFixture(t, filepath.Join(root, "net", "tcp6"), tcpHeader+
		"   0: 00000000000000000000000000000000:1F90 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 5151 1 0000000000000000 100 0 0 10 0\n")

	writeFixture(t, filepath.Join(root, "1234", "comm"), "nginx\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "1234", "fd"), 0o755))
	require.NoError(t, os.Symlink("/dev/null", filepath.Join(root, "1234", "fd", "0")))
	require.NoError(t, os.Symlink("socket:[4242]", filepath.Join(root, "1234", "fd", "3")))

	writeFixture(t, filepath.Join(root, "88", "comm"), "node\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "88", "fd"), 0o755))
	require.NoError(t, os.Symlink("socket:[5151]", filepath.Join(root, "88", "fd", "7")))

	// Diretório que não é pid é ignorado.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "self"), 0o755))

	owner, busy := portOwnerAt(root, 80)
	assert.True(t, busy)
	assert.Equal(t, "nginx (pid 1234)", owner)

	owner, busy = portOwnerAt(root, 8080)
	assert.True(t, busy)
	assert.Equal(t, "node (pid 88)", owner)

	owner, busy = portOwnerAt(root, 443)
	assert.False(t, busy, "conexão estabelecida não é escuta")
	assert.Empty(t, owner)

	// Escuta sem processo resolvível (sem permissão para ler /proc/<pid>/fd).
	writeFixture(t, filepath.Join(root, "net", "tcp6"), tcpHeader+
		"   0: 00000000000000000000000000000000:0016 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 999 1 0000000000000000 100 0 0 10 0\n")
	owner, busy = portOwnerAt(root, 22)
	assert.True(t, busy)
	assert.Equal(t, "outro processo", owner)
}

func TestPortOwner_RealProc(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	owner, busy := PortOwner(port)
	assert.True(t, busy)
	assert.Contains(t, owner, fmt.Sprintf("(pid %d)", os.Getpid()))
}

func TestDescendsAt(t *testing.T) {
	root := t.TempDir()
	stat := func(pid, ppid int) {
		// comm com espaço e parêntese, como no kernel.
		writeFixture(t, filepath.Join(root, fmt.Sprint(pid), "stat"),
			fmt.Sprintf("%d (ca ddy) ) S %d 1 1 0 -1 0\n", pid, ppid))
	}
	stat(100, 1)
	stat(101, 100)
	stat(102, 101)
	stat(200, 1)
	assert.True(t, descendsAt(root, 100, 100))
	assert.True(t, descendsAt(root, 102, 100))
	assert.False(t, descendsAt(root, 200, 100))
	assert.False(t, descendsAt(root, 999, 100), "pid inexistente")
	assert.False(t, descendsAt(root, 102, 0))
}
