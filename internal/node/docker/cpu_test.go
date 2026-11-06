package docker

import (
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/assert"
)

func amostra(cpuTotal, sistema uint64, cpus uint32) container.StatsResponse {
	return container.StatsResponse{CPUStats: container.CPUStats{
		CPUUsage:    container.CPUUsage{TotalUsage: cpuTotal},
		SystemUsage: sistema,
		OnlineCPUs:  cpus,
	}}
}

func TestCPUPercent_DockerFormula(t *testing.T) {
	prev := amostra(1000, 10000, 2)
	cur := amostra(1200, 11000, 2)
	assert.InDelta(t, 40.0, cpuPercent(prev, cur), 1e-9)

	// Delta do sistema 0 → 0.
	assert.Equal(t, 0.0, cpuPercent(prev, amostra(1200, 10000, 2)))
}

func TestCPUPercent_SemOnlineCPUsUsaPercpu(t *testing.T) {
	prev := amostra(1000, 10000, 0)
	cur := amostra(1200, 11000, 0)
	cur.CPUStats.CPUUsage.PercpuUsage = []uint64{1, 2, 3, 4}
	assert.InDelta(t, 80.0, cpuPercent(prev, cur), 1e-9)
}

func TestMemUsed_DescontaCache(t *testing.T) {
	s := container.StatsResponse{MemoryStats: container.MemoryStats{Usage: 1000, Limit: 4000, Stats: map[string]uint64{"inactive_file": 300}}}
	assert.Equal(t, Stats{CPUPercent: 0, MemUsedBytes: 700, MemLimitBytes: 4000}, toStats(container.StatsResponse{}, s))
}
