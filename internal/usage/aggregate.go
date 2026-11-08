package usage

import (
	"sort"

	"github.com/engviniciusbs/korego/internal/node/procs"
)

const maxProcsPerOwner = 50

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
	byKey := map[string]*Owner{}
	for _, p := range ps {
		ref := Classify(p, l)
		o := byKey[ref.Key]
		if o == nil {
			o = &Owner{Ref: ref}
			byKey[ref.Key] = o
		}
		o.CPUPercent += p.CPUPercent
		o.MemBytes += p.RSSBytes
		o.ProcCount++
		o.Processes = append(o.Processes, ProcView{
			PID: p.PID, Name: p.Name, Cmd: MaskCmd(p.Cmdline), User: p.User,
			CPUPercent: p.CPUPercent, MemBytes: p.RSSBytes,
		})
	}
	owners := make([]Owner, 0, len(byKey))
	for _, o := range byKey {
		if o.CPUPercent > 100 {
			o.CPUPercent = 100
		}
		sort.Slice(o.Processes, func(i, j int) bool {
			a, b := o.Processes[i], o.Processes[j]
			if a.CPUPercent != b.CPUPercent {
				return a.CPUPercent > b.CPUPercent
			}
			return a.PID < b.PID
		})
		if len(o.Processes) > maxProcsPerOwner {
			o.Processes = o.Processes[:maxProcsPerOwner]
			o.Truncated = true
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
