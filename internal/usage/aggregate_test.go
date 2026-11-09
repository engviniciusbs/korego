package usage

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/engviniciusbs/korego/internal/node/procs"
)

func TestAggregate_SumsSortsCaps(t *testing.T) {
	var ps []procs.Proc
	for i := 0; i < 60; i++ {
		ps = append(ps, procs.Proc{PID: 100 + i, PPID: 1, Name: "n", Cmdline: []string{"n"}, Cgroup: "/system.slice/big.service", CPUPercent: 3, RSSBytes: 10})
	}
	ps = append(ps,
		procs.Proc{PID: 1, PPID: 0, Cmdline: []string{"init"}, Cgroup: "/system.slice/b.service", CPUPercent: 5, RSSBytes: 100},
		procs.Proc{PID: 2, PPID: 0, Cmdline: []string{"init"}, Cgroup: "/system.slice/a.service", CPUPercent: 5, RSSBytes: 100},
		procs.Proc{PID: 3, PPID: 0, Cmdline: []string{"init"}, Cgroup: "/system.slice/c.service", CPUPercent: 5, RSSBytes: 200},
	)
	owners := Aggregate(ps, Lookup{})
	assert.Equal(t, "service:big", owners[0].Key)
	assert.Equal(t, 100.0, owners[0].CPUPercent) // 180 limitado a 100
	assert.Equal(t, uint64(600), owners[0].MemBytes)
	assert.Equal(t, 60, owners[0].ProcCount)
	assert.Len(t, owners[0].Processes, 50)
	assert.True(t, owners[0].Truncated)
	// empate de CPU: RAM desc, depois chave
	assert.Equal(t, []string{"service:big", "service:c", "service:a", "service:b"},
		[]string{owners[0].Key, owners[1].Key, owners[2].Key, owners[3].Key})
	assert.False(t, owners[1].Truncated)
}

// Só os processos que ficam depois do corte de 50 por dono passam pela máscara.
func TestAggregate_MascaraSoOsMantidos(t *testing.T) {
	var masked []string
	orig := maskCmd
	maskCmd = func(argv []string) string {
		masked = append(masked, argv[0])
		return MaskCmd(argv)
	}
	t.Cleanup(func() { maskCmd = orig })

	var ps []procs.Proc
	for i := 0; i < 60; i++ {
		// CPU decrescente com o pid: ficam os pids 100..149.
		ps = append(ps, procs.Proc{PID: 100 + i, Cmdline: []string{fmt.Sprintf("p%d", 100+i), "--token=x"}, Cgroup: "/system.slice/big.service", CPUPercent: float64(100 - i)})
	}
	o := Aggregate(ps, Lookup{})
	assert.Len(t, o[0].Processes, 50)
	assert.True(t, o[0].Truncated)
	assert.Equal(t, 60, o[0].ProcCount)
	assert.Len(t, masked, 50)
	assert.NotContains(t, masked, "p150")
	assert.NotContains(t, masked, "p159")
	assert.Contains(t, masked, "p149")
	assert.Equal(t, "p100 --token=••••", o[0].Processes[0].Cmd)
	assert.Equal(t, "p149 --token=••••", o[0].Processes[49].Cmd)
}

func TestAggregate_ProcessosOrdenadosEMascarados(t *testing.T) {
	ps := []procs.Proc{
		{PID: 9, Name: "a", Cmdline: []string{"a", "--token=x"}, Cgroup: "/system.slice/s.service", CPUPercent: 1},
		{PID: 7, Name: "b", Cmdline: []string{"b"}, Cgroup: "/system.slice/s.service", CPUPercent: 1},
		{PID: 8, Name: "c", Cmdline: []string{"c"}, Cgroup: "/system.slice/s.service", CPUPercent: 4},
	}
	o := Aggregate(ps, Lookup{})
	assert.Equal(t, []int{8, 7, 9}, []int{o[0].Processes[0].PID, o[0].Processes[1].PID, o[0].Processes[2].PID})
	assert.Equal(t, "a --token=••••", o[0].Processes[2].Cmd)
}

func TestAggregate_MontaParentDoSnapshot(t *testing.T) {
	ps := []procs.Proc{
		{PID: 500, PPID: 1, Cmdline: []string{"node"}, Cgroup: "/system.slice/pm2-root.service"},
		{PID: 501, PPID: 500, Cmdline: []string{"sh"}, Cgroup: "/system.slice/pm2-root.service"},
	}
	o := Aggregate(ps, Lookup{PM2: map[int]string{500: "app"}})
	assert.Len(t, o, 1)
	assert.Equal(t, "pm2:app", o[0].Key)
	assert.Equal(t, 2, o[0].ProcCount)
}
