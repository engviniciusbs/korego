package docker

import "github.com/moby/moby/api/types/container"

// cpuPercent aplica a fórmula do `docker stats`: delta da CPU do container /
// delta da CPU do sistema × CPUs online × 100.
func cpuPercent(prev, cur container.StatsResponse) float64 {
	cpuDelta := float64(cur.CPUStats.CPUUsage.TotalUsage) - float64(prev.CPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(cur.CPUStats.SystemUsage) - float64(prev.CPUStats.SystemUsage)
	if sysDelta <= 0 || cpuDelta <= 0 {
		return 0
	}
	cpus := float64(cur.CPUStats.OnlineCPUs)
	if cpus == 0 {
		cpus = float64(len(cur.CPUStats.CPUUsage.PercpuUsage))
	}
	return cpuDelta / sysDelta * cpus * 100
}

// memUsed é a memória em uso sem o cache de arquivos inativo, como no `docker stats`.
func memUsed(m container.MemoryStats) uint64 {
	cache := m.Stats["inactive_file"]                // cgroup v2
	if v, ok := m.Stats["total_inactive_file"]; ok { // cgroup v1
		cache = v
	}
	if cache < m.Usage {
		return m.Usage - cache
	}
	return m.Usage
}

// toStats converte uma amostra do SDK.
func toStats(prev, cur container.StatsResponse) Stats {
	return Stats{
		CPUPercent:    cpuPercent(prev, cur),
		MemUsedBytes:  memUsed(cur.MemoryStats),
		MemLimitBytes: cur.MemoryStats.Limit,
	}
}
