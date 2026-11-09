package node

import (
	"context"
	"fmt"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/metrics"
	"github.com/engviniciusbs/korego/internal/node/pm2"
	"github.com/engviniciusbs/korego/internal/node/procs"
	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

// Local é o Node que representa a máquina onde o KoreGO roda.
type Local struct {
	metrics *metrics.Local
	docker  *docker.Local
	pm2     *pm2.Local
	proxy   *proxy.Caddy
	procs   *procs.Local
}

// NewLocal cria o Node local. pm2Path vazio procura o pm2 no PATH; pm2Home
// vazio usa $PM2_HOME e, sem ela, $HOME/.pm2.
func NewLocal(pm2Path, pm2Home string) *Local {
	d := docker.NewLocal()
	return &Local{
		metrics: metrics.NewLocal(),
		docker:  d,
		pm2:     pm2.NewLocal(pm2Path, pm2Home),
		proxy:   proxy.NewCaddy(d, &proxy.AdminClient{BaseURL: "http://127.0.0.1:2019"}),
		procs:   procs.NewLocal(),
	}
}

var _ Node = (*Local)(nil)

// Metrics devolve a fonte de métricas do host local.
func (l *Local) Metrics() metrics.Source { return l.metrics }

// Docker devolve o cliente do Docker local.
func (l *Local) Docker() docker.Client { return l.docker }

// PM2 devolve o leitor do PM2 local.
func (l *Local) PM2() pm2.Reader { return l.pm2 }

// Proxy devolve o proxy Caddy local.
func (l *Local) Proxy() proxy.Proxy { return l.proxy }

// Procs devolve o leitor de processos local.
func (l *Local) Procs() procs.Source { return l.procs }

// Info lê as informações do host local.
func (l *Local) Info(ctx context.Context) (HostInfo, error) {
	h, err := host.InfoWithContext(ctx)
	if err != nil {
		return HostInfo{}, fmt.Errorf("ler informações do host: %w", err)
	}
	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return HostInfo{}, fmt.Errorf("ler memória: %w", err)
	}
	cpus, err := cpu.CountsWithContext(ctx, true)
	if err != nil {
		return HostInfo{}, fmt.Errorf("contar CPUs: %w", err)
	}
	return HostInfo{
		Hostname:         h.Hostname,
		OS:               h.OS,
		Platform:         h.Platform,
		PlatformVersion:  h.PlatformVersion,
		Kernel:           h.KernelVersion,
		Arch:             h.KernelArch,
		CPUs:             cpus,
		MemoryTotalBytes: vm.Total,
		UptimeSeconds:    h.Uptime,
	}, nil
}
