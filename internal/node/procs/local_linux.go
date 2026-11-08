//go:build linux

package procs

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Local lê os processos de /proc.
type Local struct {
	root   string // "/proc" por padrão
	passwd string // "/etc/passwd" por padrão

	mu    sync.Mutex
	track tracker
	names map[int]string // uid -> nome ("" quando sem entrada)
	users map[int]string // conteúdo do passwd, carregado uma vez
}

// NewLocal cria o leitor de /proc.
func NewLocal() *Local { return newLocalAt("/proc", "/etc/passwd") }

func newLocalAt(root, passwd string) *Local {
	return &Local{root: root, passwd: passwd, names: map[int]string{}}
}

var _ Source = (*Local)(nil)

// Snapshot lê todos os processos. Na primeira chamada CPU vale 0.
func (l *Local) Snapshot(ctx context.Context) ([]Proc, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	stat, err := os.ReadFile(filepath.Join(l.root, "stat"))
	if err != nil {
		return nil, fmt.Errorf("ler /proc/stat: %w", err)
	}
	entries, err := os.ReadDir(l.root)
	if err != nil {
		return nil, fmt.Errorf("listar /proc: %w", err)
	}
	l.track.begin(parseProcStatTotal(stat))

	var out []Proc
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		dir := filepath.Join(l.root, e.Name())
		sb, err := os.ReadFile(filepath.Join(dir, "stat"))
		if err != nil {
			continue // sumiu no meio da leitura
		}
		_, ppid, comm, ut, st, start, err := parseStat(sb)
		if err != nil {
			continue
		}
		p := Proc{PID: pid, PPID: ppid, Name: comm, UID: -1, StartTicks: start}
		if b, err := os.ReadFile(filepath.Join(dir, "status")); err == nil {
			p.RSSBytes, p.UID = parseStatusRSSUID(b)
		}
		if b, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
			p.Cmdline = parseCmdline(b)
		}
		if b, err := os.ReadFile(filepath.Join(dir, "cgroup")); err == nil {
			p.Cgroup = parseCgroup(b)
		}
		if p.UID >= 0 {
			p.User = l.userName(p.UID)
		}
		p.CPUPercent = l.track.percent(pid, start, ut+st)
		out = append(out, p)
	}
	l.track.commit()
	return out, nil
}

// userName resolve um uid pelo passwd, com cache.
func (l *Local) userName(uid int) string {
	if n, ok := l.names[uid]; ok {
		return n
	}
	if l.users == nil {
		l.users = map[int]string{}
		if f, err := os.Open(l.passwd); err == nil {
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				parts := strings.Split(sc.Text(), ":")
				if len(parts) < 3 || strings.HasPrefix(parts[0], "#") {
					continue
				}
				if id, err := strconv.Atoi(parts[2]); err == nil {
					if _, dup := l.users[id]; !dup {
						l.users[id] = parts[0]
					}
				}
			}
			f.Close()
		}
	}
	l.names[uid] = l.users[uid]
	return l.names[uid]
}
