package update

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/engviniciusbs/korego/internal/version"
)

// System é o atualizador de produção: consulta o GitHub, troca o binário em
// ExePath e dispara "korego update-finish" fora do serviço, via systemd-run.
type System struct {
	ExePath string
	Checker *Checker
	HTTP    *http.Client
	// ConfigPath é o arquivo de configuração em uso (repassado ao finalizador).
	ConfigPath string
	// Run executa comandos externos (nil = exec.Command + CombinedOutput).
	Run func(name string, args ...string) ([]byte, error)
}

// Current devolve a versão em execução.
func (s *System) Current() string { return version.Version }

// Check busca a última release para a arquitetura atual.
func (s *System) Check(ctx context.Context) (Release, error) {
	c := s.Checker
	if c == nil {
		c = &Checker{}
	}
	return c.Latest(ctx, runtime.GOARCH)
}

// Apply baixa, verifica e instala a release.
func (s *System) Apply(ctx context.Context, rel Release) error {
	hc := s.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Minute}
	}
	return Apply(ctx, rel, s.ExePath, hc)
}

// StartFinish inicia o finalizador como unidade transitória do systemd, para
// que sobreviva ao reinício do serviço korego. Ele roda a partir do binário
// anterior (.prev, conhecido como bom), não do novo.
func (s *System) StartFinish() error {
	run := s.Run
	if run == nil {
		run = func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		}
	}
	cfg := s.ConfigPath
	if cfg == "" {
		cfg = "/etc/korego/config.toml"
	}
	out, err := run("systemd-run", "--unit=korego-update", "--collect",
		s.ExePath+".prev", "update-finish", "--exe", s.ExePath, "--config", cfg)
	if err != nil {
		return fmt.Errorf("systemd-run: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
