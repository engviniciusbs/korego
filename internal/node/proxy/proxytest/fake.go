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
// pending. As rotas da última Apply bem-sucedida ficam em LastApplied.
type Fake struct {
	EnsureFn func(ctx context.Context) proxy.Status
	ApplyFn  func(ctx context.Context, routes []proxy.Route) error
	InSyncFn func(ctx context.Context, routes []proxy.Route) (bool, error)
	CertFn   func(ctx context.Context, host string) proxy.CertInfo

	mu      sync.Mutex
	applied []proxy.Route
	applies int
}

var _ proxy.Proxy = (*Fake)(nil)

// Ensure chama EnsureFn.
func (f *Fake) Ensure(ctx context.Context) proxy.Status {
	if f.EnsureFn == nil {
		return proxy.Status{State: proxy.StateActive}
	}
	return f.EnsureFn(ctx)
}

// Apply chama ApplyFn e, sem erro, grava as rotas.
func (f *Fake) Apply(ctx context.Context, routes []proxy.Route) error {
	if f.ApplyFn != nil {
		if err := f.ApplyFn(ctx, routes); err != nil {
			return err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = slices.Clone(routes)
	f.applies++
	return nil
}

// InSync chama InSyncFn.
func (f *Fake) InSync(ctx context.Context, routes []proxy.Route) (bool, error) {
	if f.InSyncFn == nil {
		return true, nil
	}
	return f.InSyncFn(ctx, routes)
}

// Cert chama CertFn.
func (f *Fake) Cert(ctx context.Context, host string) proxy.CertInfo {
	if f.CertFn == nil {
		return proxy.CertInfo{State: proxy.CertPending}
	}
	return f.CertFn(ctx, host)
}

// LastApplied devolve uma cópia das rotas da última Apply bem-sucedida.
func (f *Fake) LastApplied() []proxy.Route {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.applied)
}

// Applies conta as chamadas bem-sucedidas de Apply.
func (f *Fake) Applies() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.applies
}
