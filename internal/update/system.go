package update

import (
	"context"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/engviniciusbs/korego/internal/version"
)

// System é o atualizador de produção: consulta o GitHub, troca o binário em
// ExePath e dispara "korego update-finish" fora do serviço, via systemd-run.
type System struct {
	ExePath string
	Checker *Checker
	HTTP    *http.Client
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
// que sobreviva ao reinício do serviço korego.
func (s *System) StartFinish() error {
	return exec.Command("systemd-run", "--unit=korego-update", "--collect", s.ExePath, "update-finish").Run()
}
