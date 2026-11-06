package metrics

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

// Local lê as métricas da máquina onde o KoreGO roda. Guarda a leitura
// anterior para calcular CPU e taxas de rede pela diferença.
type Local struct {
	mu       sync.Mutex
	havePrev bool
	prevAt   time.Time
	prevCPU  cpu.TimesStat
	prevRx   uint64
	prevTx   uint64
}

// NewLocal cria a fonte de métricas local.
func NewLocal() *Local { return &Local{} }

var _ Source = (*Local)(nil)

// Sample lê uma amostra. Na primeira chamada CPU e taxas valem 0.
func (l *Local) Sample(ctx context.Context) (Sample, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	s := Sample{Time: now}

	times, err := cpu.TimesWithContext(ctx, false)
	if err != nil {
		return Sample{}, fmt.Errorf("ler tempos de CPU: %w", err)
	}
	if len(times) == 0 {
		return Sample{}, errors.New("ler tempos de CPU: nenhuma leitura")
	}
	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return Sample{}, fmt.Errorf("ler memória: %w", err)
	}
	s.MemTotalBytes = vm.Total
	s.MemUsedBytes = vm.Total - vm.Available

	// Sem load average (Windows) os valores ficam em zero.
	if avg, err := load.AvgWithContext(ctx); err == nil && avg != nil {
		s.Load1, s.Load5, s.Load15 = avg.Load1, avg.Load5, avg.Load15
	}

	counters, err := net.IOCountersWithContext(ctx, true)
	if err != nil {
		return Sample{}, fmt.Errorf("ler contadores de rede: %w", err)
	}
	var rx, tx uint64
	for _, c := range counters {
		if includeInterface(c.Name) {
			rx += c.BytesRecv
			tx += c.BytesSent
		}
	}

	parts, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return Sample{}, fmt.Errorf("listar partições: %w", err)
	}
	s.Disks = []Disk{}
	for _, p := range filterDisks(parts) {
		u, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil {
			continue // ponto inacessível (ex.: unidade sem mídia)
		}
		s.Disks = append(s.Disks, Disk{
			Mount: p.Mountpoint, FSType: p.Fstype,
			UsedBytes: u.Used, TotalBytes: u.Total,
		})
	}

	if l.havePrev {
		elapsed := now.Sub(l.prevAt)
		s.CPUPercent = cpuPercent(l.prevCPU, times[0])
		s.NetRxBytesPerSec = rate(l.prevRx, rx, elapsed)
		s.NetTxBytesPerSec = rate(l.prevTx, tx, elapsed)
	}
	l.havePrev, l.prevAt, l.prevCPU, l.prevRx, l.prevTx = true, now, times[0], rx, tx
	return s, nil
}
