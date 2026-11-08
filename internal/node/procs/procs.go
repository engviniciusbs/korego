// Package procs lê os processos da máquina, com CPU calculada por intervalo.
package procs

import "context"

// Proc é uma leitura de processo.
type Proc struct {
	PID, PPID  int
	Name       string   // comm
	Cmdline    []string // argv, cru (a máscara é aplicada em usage)
	UID        int      // -1 quando desconhecido
	User       string   // nome resolvido, "" quando desconhecido
	Cgroup     string   // caminho unificado (v2), ou o v1 que tem id docker; "" fora do Linux
	CPUPercent float64  // % da máquina inteira desde a leitura anterior, 0..100
	RSSBytes   uint64
	StartTicks uint64 // campo 22 do stat; identifica reuso de PID
}

// Source lê todos os processos.
type Source interface {
	Snapshot(ctx context.Context) ([]Proc, error)
}

// prevEntry é o que se lembra de um PID entre duas leituras.
type prevEntry struct{ start, ticks uint64 }

// tracker calcula CPU% pela diferença entre leituras. Não é seguro para uso
// concorrente: quem o usa deve serializar com um mutex.
type tracker struct {
	havePrev  bool
	prevTotal uint64
	prev      map[int]prevEntry
	next      map[int]prevEntry
	dTotal    uint64
}

// begin inicia uma leitura com o total de ticks atual da máquina.
func (t *tracker) begin(total uint64) {
	t.next = make(map[int]prevEntry, len(t.prev))
	t.dTotal = 0
	if t.havePrev && total > t.prevTotal {
		t.dTotal = total - t.prevTotal
	}
	t.prevTotal = total
}

// percent devolve o CPU% de um processo e registra-o para a próxima leitura.
// PID novo, PID reaproveitado (start diferente) ou contador que recuou valem 0.
func (t *tracker) percent(pid int, start, ticks uint64) float64 {
	t.next[pid] = prevEntry{start, ticks}
	p, ok := t.prev[pid]
	if !ok || p.start != start || ticks < p.ticks || t.dTotal == 0 {
		return 0
	}
	pct := float64(ticks-p.ticks) / float64(t.dTotal) * 100
	if pct > 100 {
		pct = 100
	}
	return pct
}

// commit fecha a leitura; PIDs que sumiram são esquecidos.
func (t *tracker) commit() {
	t.prev, t.next, t.havePrev = t.next, nil, true
}
