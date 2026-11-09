//go:build linux

package migrate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Systemd implementa ServiceManager chamando systemctl, com 15 s por chamada.
type Systemd struct{}

var _ ServiceManager = Systemd{}

func (Systemd) run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, systemctlTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if ctx.Err() != nil {
		// Morto pelo prazo: é erro, nunca "inativo" (não embrulha o ExitError).
		return text, fmt.Errorf("systemctl %s: tempo esgotado (%s): %v", strings.Join(args, " "), systemctlTimeout, ctx.Err())
	}
	if err != nil {
		if text != "" {
			return text, fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, text)
		}
		return text, fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
	}
	return text, nil
}

func service(unit string) string { return unit + ".service" }

// Stop roda systemctl stop.
func (s Systemd) Stop(ctx context.Context, unit string) error {
	_, err := s.run(ctx, "stop", service(unit))
	return err
}

// Start roda systemctl start.
func (s Systemd) Start(ctx context.Context, unit string) error {
	_, err := s.run(ctx, "start", service(unit))
	return err
}

// Enable roda systemctl enable.
func (s Systemd) Enable(ctx context.Context, unit string) error {
	_, err := s.run(ctx, "enable", service(unit))
	return err
}

// Disable roda systemctl disable.
func (s Systemd) Disable(ctx context.Context, unit string) error {
	_, err := s.run(ctx, "disable", service(unit))
	return err
}

// Active roda systemctl is-active: "active" é true; os demais estados
// (inactive, failed, activating...) saem com código diferente de zero e são
// false sem erro.
func (s Systemd) Active(ctx context.Context, unit string) (bool, error) {
	out, err := s.run(ctx, "is-active", service(unit))
	if err == nil {
		return out == "active", nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, err
}

// Exists diz se o arquivo da unit (FragmentPath) existe.
func (s Systemd) Exists(ctx context.Context, unit string) bool {
	path, err := s.run(ctx, "show", "-p", "FragmentPath", "--value", service(unit))
	if err != nil || path == "" {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// UnitForProcess usa o mapa do pacote.
func (Systemd) UnitForProcess(process string) (string, bool) { return UnitForProcess(process) }
