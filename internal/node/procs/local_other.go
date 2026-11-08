//go:build !linux

package procs

import (
	"context"
	"fmt"
	"sync"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/process"
)

// Local lê os processos via gopsutil (fora do Linux o cgroup fica vazio).
type Local struct {
	mu    sync.Mutex
	track tracker
}

// NewLocal cria o leitor de processos.
func NewLocal() *Local { return &Local{} }

var _ Source = (*Local)(nil)

func ms(sec float64) uint64 { return uint64(sec * 1000) }

// Snapshot lê todos os processos. Na primeira chamada CPU vale 0.
func (l *Local) Snapshot(ctx context.Context) ([]Proc, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	times, err := cpu.TimesWithContext(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("ler tempos de CPU: %w", err)
	}
	if len(times) == 0 {
		return nil, fmt.Errorf("ler tempos de CPU: nenhuma leitura")
	}
	t := times[0]
	l.track.begin(ms(t.User + t.System + t.Idle + t.Nice + t.Iowait + t.Irq + t.Softirq + t.Steal))

	ps, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("listar processos: %w", err)
	}
	var out []Proc
	for _, pr := range ps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pt, err := pr.TimesWithContext(ctx)
		if err != nil {
			continue // sem acesso ou já terminou
		}
		name, _ := pr.NameWithContext(ctx)
		p := Proc{PID: int(pr.Pid), Name: name, UID: -1}
		if pp, err := pr.PpidWithContext(ctx); err == nil {
			p.PPID = int(pp)
		}
		if args, err := pr.CmdlineSliceWithContext(ctx); err == nil {
			p.Cmdline = args
		}
		if mi, err := pr.MemoryInfoWithContext(ctx); err == nil && mi != nil {
			p.RSSBytes = mi.RSS
		}
		if ct, err := pr.CreateTimeWithContext(ctx); err == nil && ct > 0 {
			p.StartTicks = uint64(ct)
		}
		p.CPUPercent = l.track.percent(p.PID, p.StartTicks, ms(pt.User+pt.System))
		out = append(out, p)
	}
	l.track.commit()
	return out, nil
}
