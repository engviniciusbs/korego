// Package proxytest oferece um proxy.Proxy falso para testes.
package proxytest

import (
	"context"
	"slices"
	"sync"

	"github.com/engviniciusbs/korego/internal/node/proxy"
)

// Fake implementa proxy.Proxy delegando às funções informadas. Sem função:
// Ensure devolve active, Apply aceita, InSync devolve true e Cert devolve
// pending; Switch aceita. Os sites, o modo e as faixas cf da última Apply bem-sucedida ficam
// em LastApplied, LastMode e LastCF.
type Fake struct {
	EnsureFn func(ctx context.Context) proxy.Status
	ApplyFn  func(ctx context.Context, sites []proxy.Site, mode proxy.Listen, cf []string) error
	InSyncFn func(ctx context.Context, sites []proxy.Site, mode proxy.Listen, cf []string) (bool, error)
	CertFn   func(ctx context.Context, host string) proxy.CertInfo
	SwitchFn func(ctx context.Context, sites []proxy.Site, mode proxy.Listen, cf []string) error

	mu       sync.Mutex
	applied  []proxy.Site
	mode     proxy.Listen
	cf       []string
	applies  int
	switches int
}

var _ proxy.Proxy = (*Fake)(nil)

// Ensure chama EnsureFn.
func (f *Fake) Ensure(ctx context.Context) proxy.Status {
	if f.EnsureFn == nil {
		return proxy.Status{State: proxy.StateActive}
	}
	return f.EnsureFn(ctx)
}

// Apply chama ApplyFn e, sem erro, grava os sites, o modo e as faixas cf.
func (f *Fake) Apply(ctx context.Context, sites []proxy.Site, mode proxy.Listen, cf []string) error {
	if f.ApplyFn != nil {
		if err := f.ApplyFn(ctx, sites, mode, cf); err != nil {
			return err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = slices.Clone(sites)
	f.mode = mode
	f.cf = slices.Clone(cf)
	f.applies++
	return nil
}

// Switch chama SwitchFn e, sem erro, grava os sites, o modo e as faixas cf
// como Apply (conta em Switches, não em Applies).
func (f *Fake) Switch(ctx context.Context, sites []proxy.Site, mode proxy.Listen, cf []string) error {
	if f.SwitchFn != nil {
		if err := f.SwitchFn(ctx, sites, mode, cf); err != nil {
			return err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = slices.Clone(sites)
	f.mode = mode
	f.cf = slices.Clone(cf)
	f.switches++
	return nil
}

// Switches conta as chamadas bem-sucedidas de Switch.
func (f *Fake) Switches() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.switches
}

// InSync chama InSyncFn.
func (f *Fake) InSync(ctx context.Context, sites []proxy.Site, mode proxy.Listen, cf []string) (bool, error) {
	if f.InSyncFn == nil {
		return true, nil
	}
	return f.InSyncFn(ctx, sites, mode, cf)
}

// Cert chama CertFn.
func (f *Fake) Cert(ctx context.Context, host string) proxy.CertInfo {
	if f.CertFn == nil {
		return proxy.CertInfo{State: proxy.CertPending}
	}
	return f.CertFn(ctx, host)
}

// LastApplied devolve uma cópia dos sites da última Apply bem-sucedida.
func (f *Fake) LastApplied() []proxy.Site {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.applied)
}

// LastMode devolve o modo de escuta da última Apply bem-sucedida.
func (f *Fake) LastMode() proxy.Listen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode
}

// LastCF devolve uma cópia das faixas cf da última Apply bem-sucedida.
func (f *Fake) LastCF() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.cf)
}

// Applies conta as chamadas bem-sucedidas de Apply.
func (f *Fake) Applies() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.applies
}
