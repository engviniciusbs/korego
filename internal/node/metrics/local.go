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

// netCounter guarda os contadores acumulados de uma interface.
type netCounter struct{ rx, tx uint64 }

// Local lê as métricas da máquina onde o KoreGO roda. Guarda a leitura
// anterior para calcular CPU e taxas de rede pela diferença.
type Local struct {
	mu       sync.Mutex
	havePrev bool
	prevAt   time.Time
	prevCPU  cpu.TimesStat
	prevNet  map[string]netCounter

	// Pontos de injeção para testes.
	now   func() time.Time
	cpuFn func(ctx context.Context) (cpu.TimesStat, error)
	netFn func(ctx context.Context) (map[string]netCounter, error)
}

// NewLocal cria a fonte de métricas local.
func NewLocal() *Local {
	return &Local{now: time.Now, cpuFn: readCPU, netFn: readNet}
}

func readCPU(ctx context.Context) (cpu.TimesStat, error) {
	times, err := cpu.TimesWithContext(ctx, false)
	if err != nil {
		return cpu.TimesStat{}, err
	}
	if len(times) == 0 {
		return cpu.TimesStat{}, errors.New("nenhuma leitura")
	}
	return times[0], nil
}

// readNet lê os contadores por interface, já sem as interfaces excluídas.
func readNet(ctx context.Context) (map[string]netCounter, error) {
	counters, err := net.IOCountersWithContext(ctx, true)
	if err != nil {
		return nil, err
	}
	out := make(map[string]netCounter, len(counters))
	for _, c := range counters {
		if includeInterface(c.Name) {
			out[c.Name] = netCounter{rx: c.BytesRecv, tx: c.BytesSent}
		}
	}
	return out, nil
}

var _ Source = (*Local)(nil)

// Sample lê uma amostra. Na primeira chamada CPU e taxas valem 0.
func (l *Local) Sample(ctx context.Context) (Sample, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	s := Sample{Time: now}

	curCPU, err := l.cpuFn(ctx)
	if err != nil {
		return Sample{}, fmt.Errorf("ler tempos de CPU: %w", err)
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

	curNet, err := l.netFn(ctx)
	if err != nil {
		return Sample{}, fmt.Errorf("ler contadores de rede: %w", err)
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
		s.CPUPercent = cpuPercent(l.prevCPU, curCPU)
		// Delta por interface: interface nova ou com contador que recuou
		// contribui 0, sem esconder nem inflar o tráfego das demais.
		var dRx, dTx uint64
		for name, c := range curNet {
			p, ok := l.prevNet[name]
			if !ok {
				continue
			}
			if c.rx >= p.rx {
				dRx += c.rx - p.rx
			}
			if c.tx >= p.tx {
				dTx += c.tx - p.tx
			}
		}
		s.NetRxBytesPerSec = rate(0, dRx, elapsed)
		s.NetTxBytesPerSec = rate(0, dTx, elapsed)
	}
	l.havePrev, l.prevAt, l.prevCPU, l.prevNet = true, now, curCPU, curNet
	return s, nil
}
