package pm2

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	cacheTTL    = 10 * time.Second
	execTimeout = 10 * time.Second
)

// Local lê o PM2 da máquina local via `pm2 jlist`.
type Local struct {
	path     string
	home     string // PM2_HOME configurado ("" = ambiente, depois $HOME/.pm2)
	procRoot string // raiz do /proc (trocada nos testes)
	now      func() time.Time

	mu       sync.Mutex
	cached   Snapshot
	cachedAt time.Time
	hasCache bool
}

var _ Reader = (*Local)(nil)

// NewLocal cria o leitor. path vazio procura "pm2" no PATH. home é o
// PM2_HOME do usuário que roda o PM2; vazio usa a variável PM2_HOME e, sem
// ela, $HOME/.pm2.
func NewLocal(path, home string) *Local {
	return &Local{path: path, home: home, procRoot: "/proc", now: time.Now}
}

func (l *Local) binary() (string, bool) {
	if l.path != "" {
		if st, err := os.Stat(l.path); err != nil || st.IsDir() {
			return "", false
		}
		return l.path, true
	}
	p, err := exec.LookPath("pm2")
	if err != nil {
		return "", false
	}
	return p, true
}

// Snapshot devolve os processos do PM2, em cache por 10 s. Sem binário ou sem
// daemon ativo, não executa o pm2 (que iniciaria o daemon).
func (l *Local) Snapshot(ctx context.Context) (Snapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	bin, ok := l.binary()
	if !ok {
		return Snapshot{Processes: []Process{}}, nil
	}
	if !l.daemonRunning() {
		return Snapshot{Available: true, Processes: []Process{}}, nil
	}
	if l.hasCache && l.now().Sub(l.cachedAt) < cacheTTL {
		return l.cached, nil
	}

	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "jlist")
	// O diretório do pm2 vai à frente do PATH para achar o node (nvm).
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if l.home != "" {
		cmd.Env = append(cmd.Env, "PM2_HOME="+l.home)
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return Snapshot{}, fmt.Errorf("executar pm2 jlist: %w", err)
	}
	now := l.now()
	ps, err := parseJList(stdout.Bytes(), now)
	if err != nil {
		return Snapshot{}, err
	}
	l.cached = Snapshot{Available: true, DaemonRunning: true, Processes: ps}
	l.cachedAt = now
	l.hasCache = true
	return l.cached, nil
}

// pm2Home devolve o diretório do PM2: o configurado, senão $PM2_HOME, senão
// $HOME/.pm2.
func (l *Local) pm2Home() (string, bool) {
	if l.home != "" {
		return l.home, true
	}
	if home := os.Getenv("PM2_HOME"); home != "" {
		return home, true
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(h, ".pm2"), true
}

// daemonRunning confere o pid em <PM2_HOME>/pm2.pid. No Linux, exige também
// que /proc/<pid>/cmdline seja o God Daemon do PM2 ("PM2 vX.Y.Z: God
// Daemon (...)"): um pid file antigo cujo pid foi reaproveitado depois de um
// reboot faria o jlist iniciar um daemon novo.
func (l *Local) daemonRunning() bool {
	home, ok := l.pm2Home()
	if !ok {
		return false
	}
	b, err := os.ReadFile(filepath.Join(home, "pm2.pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return false
	}
	if !processExists(pid) {
		return false
	}
	if runtime.GOOS != "linux" {
		return true
	}
	cmdline, err := os.ReadFile(filepath.Join(l.procRoot, strconv.Itoa(pid), "cmdline"))
	return err == nil && bytes.Contains(cmdline, []byte("PM2"))
}
