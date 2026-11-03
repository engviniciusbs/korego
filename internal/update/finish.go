package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrRolledBack indica que a nova versão não ficou saudável e a anterior foi restaurada.
var ErrRolledBack = errors.New("nova versão não respondeu; versão anterior restaurada")

// Finisher reinicia o serviço após uma atualização e reverte se ele não ficar saudável.
type Finisher struct {
	Restart func(ctx context.Context) error
	Health  func(ctx context.Context) error
	Timeout time.Duration // padrão 30s
	// Interval é o intervalo entre checagens de saúde (padrão 1s).
	Interval time.Duration
	ExePath  string
}

// Run reinicia, aguarda a saúde até Timeout e, se falhar, restaura exePath+".prev".
func (f *Finisher) Run(ctx context.Context) error {
	timeout, interval := f.Timeout, f.Interval
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if interval <= 0 {
		interval = time.Second
	}
	restartErr := f.Restart(ctx)
	if restartErr == nil {
		if f.waitHealthy(ctx, timeout, interval) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	if err := f.rollback(); err != nil {
		return err
	}
	if err := f.Restart(ctx); err != nil {
		return fmt.Errorf("reiniciar após rollback: %w", err)
	}
	if restartErr != nil {
		return fmt.Errorf("%w (reinício falhou: %v)", ErrRolledBack, restartErr)
	}
	return ErrRolledBack
}

// rollback restaura .prev em ExePath de forma atômica, guardando o binário
// com falha em .failed. Sem .prev, não toca em ExePath.
func (f *Finisher) rollback() error {
	prev := f.ExePath + ".prev"
	if _, err := os.Stat(prev); err != nil {
		return fmt.Errorf("binário anterior %s indisponível, rollback impossível: %w", prev, err)
	}
	if err := linkOrCopy(f.ExePath, f.ExePath+".failed"); err != nil {
		return fmt.Errorf("guardar binário com falha: %w", err)
	}
	if err := os.Rename(prev, f.ExePath); err != nil {
		return fmt.Errorf("restaurar binário anterior: %w", err)
	}
	syncDir(filepath.Dir(f.ExePath))
	return nil
}

func (f *Finisher) waitHealthy(ctx context.Context, timeout, interval time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-tick.C:
			if f.Health(ctx) == nil {
				return true
			}
		}
	}
}
