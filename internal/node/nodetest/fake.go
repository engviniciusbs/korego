// Package nodetest oferece um node.Node falso para testes.
package nodetest

import (
	"context"

	"github.com/engviniciusbs/korego/internal/node"
)

// Fake implementa node.Node delegando às funções informadas.
type Fake struct {
	InfoFn func(ctx context.Context) (node.HostInfo, error)
}

var _ node.Node = (*Fake)(nil)

// Info chama InfoFn.
func (f *Fake) Info(ctx context.Context) (node.HostInfo, error) { return f.InfoFn(ctx) }
