package metrics

import (
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
)

// cpuPercent calcula o percentual ocupado entre duas leituras de tempos de CPU.
func cpuPercent(prev, cur cpu.TimesStat) float64 {
	busy := func(t cpu.TimesStat) float64 { return t.Total() - t.Idle - t.Iowait }
	dTotal := cur.Total() - prev.Total()
	if dTotal <= 0 {
		return 0
	}
	p := (busy(cur) - busy(prev)) / dTotal * 100
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// rate calcula bytes por segundo; contador que recuou ou intervalo nulo dão 0.
func rate(prev, cur uint64, elapsed time.Duration) float64 {
	if cur < prev || elapsed <= 0 {
		return 0
	}
	return float64(cur-prev) / elapsed.Seconds()
}

// includeInterface exclui interfaces que contariam o tráfego em dobro.
func includeInterface(name string) bool {
	if name == "lo" {
		return false
	}
	for _, p := range []string{"docker", "br-", "veth"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

var ignoredFS = map[string]bool{
	"squashfs": true, "overlay": true, "tmpfs": true, "devtmpfs": true,
}

func underPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// filterDisks descarta sistemas de arquivos virtuais e montagens repetidas
// do mesmo dispositivo, mantendo a de caminho mais curto.
func filterDisks(parts []disk.PartitionStat) []disk.PartitionStat {
	var out []disk.PartitionStat
	idx := map[string]int{} // dispositivo -> posição em out
	for _, p := range parts {
		if ignoredFS[p.Fstype] ||
			underPrefix(p.Mountpoint, "/var/lib/docker") ||
			underPrefix(p.Mountpoint, "/snap") {
			continue
		}
		if i, ok := idx[p.Device]; ok {
			if len(p.Mountpoint) < len(out[i].Mountpoint) {
				out[i] = p
			}
			continue
		}
		idx[p.Device] = len(out)
		out = append(out, p)
	}
	return out
}
