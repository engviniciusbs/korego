//go:build linux

package proxy

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// tcpListen é o estado TCP_LISTEN em /proc/net/tcp.
const tcpListen = "0A"

// PortOwner diz quem escuta na porta TCP, em qualquer IP (v4 ou v6), como
// "<comm> (pid N)". Se a escuta existe mas o processo não pôde ser resolvido
// (sem permissão para ler /proc/<pid>/fd), devolve "outro processo".
func PortOwner(port int) (string, bool) { return portOwnerAt("/proc", port) }

func portOwnerAt(root string, port int) (string, bool) {
	inodes := map[string]bool{}
	for _, f := range []string{"tcp", "tcp6"} {
		data, err := os.ReadFile(filepath.Join(root, "net", f))
		if err != nil {
			continue
		}
		for _, ino := range listenInodes(data, port) {
			inodes[ino] = true
		}
	}
	if len(inodes) == 0 {
		return "", false
	}
	if pid, ok := socketPid(root, inodes); ok {
		comm, err := os.ReadFile(filepath.Join(root, strconv.Itoa(pid), "comm"))
		name := strings.TrimSpace(string(comm))
		if err != nil || name == "" {
			name = "processo"
		}
		return fmt.Sprintf("%s (pid %d)", name, pid), true
	}
	return "outro processo", true
}

// listenInodes devolve os inodes dos sockets em LISTEN na porta, a partir do
// conteúdo de /proc/net/tcp ou /proc/net/tcp6.
func listenInodes(data []byte, port int) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	first := true
	for sc.Scan() {
		if first { // cabeçalho
			first = false
			continue
		}
		f := strings.Fields(sc.Text())
		if len(f) < 10 || f[3] != tcpListen {
			continue
		}
		_, hexPort, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		p, err := strconv.ParseUint(hexPort, 16, 16)
		if err != nil || int(p) != port {
			continue
		}
		if f[9] != "0" {
			out = append(out, f[9])
		}
	}
	return out
}

// socketPid procura, em /proc/<pid>/fd, um link "socket:[<inode>]" para um
// dos inodes. Devolve o menor pid encontrado (a ordem de ReadDir é por nome).
func socketPid(root string, inodes map[string]bool) (int, bool) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, false
	}
	best := 0
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		fdDir := filepath.Join(root, e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil || !strings.HasPrefix(target, "socket:[") {
				continue
			}
			ino := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
			if inodes[ino] && (best == 0 || pid < best) {
				best = pid
			}
		}
	}
	return best, best != 0
}
