// Package nodetest oferece um node.Node falso para testes.
package nodetest

import (
	"context"

	"github.com/engviniciusbs/korego/internal/node"
	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/metrics"
	"github.com/engviniciusbs/korego/internal/node/pm2"
)

// Fake implementa node.Node delegando às funções informadas.
type Fake struct {
	InfoFn        func(ctx context.Context) (node.HostInfo, error)
	MetricsSource metrics.Source
	DockerClient  docker.Client
	PM2Reader     pm2.Reader
}

var _ node.Node = (*Fake)(nil)

// Info chama InfoFn.
func (f *Fake) Info(ctx context.Context) (node.HostInfo, error) { return f.InfoFn(ctx) }

// Metrics devolve MetricsSource.
func (f *Fake) Metrics() metrics.Source { return f.MetricsSource }

// Docker devolve DockerClient.
func (f *Fake) Docker() docker.Client { return f.DockerClient }

// PM2 devolve PM2Reader.
func (f *Fake) PM2() pm2.Reader { return f.PM2Reader }
