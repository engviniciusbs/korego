package usage

import (
	"sort"

	"github.com/engviniciusbs/korego/internal/node/procs"
)

const maxProcsPerOwner = 50

// maskCmd é MaskCmd; variável só para o teste contar as chamadas.
var maskCmd = MaskCmd

// ProcView é um processo como mostrado dentro de um dono.
type ProcView struct {
	PID             int
	Name, Cmd, User string
	CPUPercent      float64
	MemBytes        uint64
}

// Owner é um dono com o consumo somado.
type Owner struct {
	Ref
	CPUPercent float64
	MemBytes   uint64
	ProcCount  int
	Processes  []ProcView // por CPU desc, no máximo 50
	Truncated  bool
}

// Aggregate agrupa os processos por dono. Sem l.Parent, monta o mapa de
// ancestrais a partir da própria leitura.
func Aggregate(ps []procs.Proc, l Lookup) []Owner {
	if l.Parent == nil {
		l.Parent = make(map[int]int, len(ps))
		for _, p := range ps {
			l.Parent[p.PID] = p.PPID
		}
	}
	type group struct {
		o  *Owner
		ps []procs.Proc
	}
	byKey := map[string]*group{}
	for _, p := range ps {
		ref := Classify(p, l)
		g := byKey[ref.Key]
		if g == nil {
			g = &group{o: &Owner{Ref: ref}}
			byKey[ref.Key] = g
		}
		g.o.CPUPercent += p.CPUPercent
		g.o.MemBytes += p.RSSBytes
		g.o.ProcCount++
		g.ps = append(g.ps, p)
	}
	owners := make([]Owner, 0, len(byKey))
	for _, g := range byKey {
		o := g.o
		if o.CPUPercent > 100 {
			o.CPUPercent = 100
		}
		sort.Slice(g.ps, func(i, j int) bool {
			a, b := g.ps[i], g.ps[j]
			if a.CPUPercent != b.CPUPercent {
				return a.CPUPercent > b.CPUPercent
			}
			return a.PID < b.PID
		})
		if len(g.ps) > maxProcsPerOwner {
			g.ps = g.ps[:maxProcsPerOwner]
			o.Truncated = true
		}
		// A máscara só roda nos processos que ficam depois do corte.
		o.Processes = make([]ProcView, 0, len(g.ps))
		for _, p := range g.ps {
			o.Processes = append(o.Processes, ProcView{
				PID: p.PID, Name: p.Name, Cmd: maskCmd(p.Cmdline), User: p.User,
				CPUPercent: p.CPUPercent, MemBytes: p.RSSBytes,
			})
		}
		owners = append(owners, *o)
	}
	sort.Slice(owners, func(i, j int) bool {
		a, b := owners[i], owners[j]
		if a.CPUPercent != b.CPUPercent {
			return a.CPUPercent > b.CPUPercent
		}
		if a.MemBytes != b.MemBytes {
			return a.MemBytes > b.MemBytes
		}
		return a.Key < b.Key
	})
	return owners
}
