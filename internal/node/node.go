// Package node define a fronteira entre o KoreGO e a máquina que ele gerencia.
package node

import (
	"context"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/metrics"
	"github.com/engviniciusbs/korego/internal/node/pm2"
	"github.com/engviniciusbs/korego/internal/node/procs"
	"github.com/engviniciusbs/korego/internal/node/proxy"
)

// HostInfo descreve o host.
type HostInfo struct {
	Hostname         string `json:"hostname"`
	OS               string `json:"os"`
	Platform         string `json:"platform"`
	PlatformVersion  string `json:"platformVersion"`
	Kernel           string `json:"kernel"`
	Arch             string `json:"arch"`
	CPUs             int    `json:"cpus"`
	MemoryTotalBytes uint64 `json:"memoryTotalBytes"`
	UptimeSeconds    uint64 `json:"uptimeSeconds"`
}

// Node é uma máquina gerenciada (nesta etapa, apenas a local).
type Node interface {
	Info(ctx context.Context) (HostInfo, error)
	// Metrics devolve a fonte de métricas do host.
	Metrics() metrics.Source
	// Docker devolve o cliente Docker do node.
	Docker() docker.Client
	// PM2 devolve o leitor (somente leitura) do PM2.
	PM2() pm2.Reader
	// Proxy devolve o proxy reverso (Caddy) do node.
	Proxy() proxy.Proxy
	// Procs devolve a fonte de leitura de processos do host.
	Procs() procs.Source
}
