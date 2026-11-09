//go:build !linux

package migrate

import (
	"context"
	"errors"
)

// Systemd é o ServiceManager real; fora do Linux não há systemd e toda
// chamada falha (Exists é false, então nenhuma troca começa).
type Systemd struct{}

var _ ServiceManager = Systemd{}

var errNoSystemd = errors.New("systemd indisponível nesta plataforma")

// Stop falha fora do Linux.
func (Systemd) Stop(context.Context, string) error { return errNoSystemd }

// Start falha fora do Linux.
func (Systemd) Start(context.Context, string) error { return errNoSystemd }

// Enable falha fora do Linux.
func (Systemd) Enable(context.Context, string) error { return errNoSystemd }

// Disable falha fora do Linux.
func (Systemd) Disable(context.Context, string) error { return errNoSystemd }

// Active falha fora do Linux.
func (Systemd) Active(context.Context, string) (bool, error) { return false, errNoSystemd }

// Exists é sempre false fora do Linux.
func (Systemd) Exists(context.Context, string) bool { return false }

// UnitForProcess usa o mapa do pacote.
func (Systemd) UnitForProcess(process string) (string, bool) { return UnitForProcess(process) }
