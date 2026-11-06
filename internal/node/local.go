package node

import (
	"context"
	"fmt"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

// Local é o Node que representa a máquina onde o KoreGO roda.
type Local struct{}

// NewLocal cria o Node local.
func NewLocal() *Local { return &Local{} }

var _ Node = (*Local)(nil)

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
