package procs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// snap simula uma leitura completa: begin, percent de cada PID e commit.
func snap(t *tracker, total uint64, ps map[int][2]uint64) map[int]float64 {
	t.begin(total)
	out := map[int]float64{}
	for pid, v := range ps {
		out[pid] = t.percent(pid, v[0], v[1])
	}
	t.commit()
	return out
}

func TestTracker_NewPIDBackwardsClampAndPrune(t *testing.T) {
	var tr tracker
	first := snap(&tr, 1000, map[int][2]uint64{1: {5, 100}, 2: {6, 100}, 3: {7, 100}})
	assert.Zero(t, first[1], "primeira leitura vale 0")

	got := snap(&tr, 1200, map[int][2]uint64{
		1: {5, 150},  // Δ50 de 200 => 25%
		2: {6, 50},   // contador recuou => 0
		3: {7, 1000}, // Δ900 de 200 => limitado a 100
		4: {8, 999},  // PID visto pela 1ª vez na 2ª leitura => 0
	})
	assert.InDelta(t, 25.0, got[1], 1e-9)
	assert.Zero(t, got[2])
	assert.Equal(t, 100.0, got[3])
	assert.Zero(t, got[4])

	// PIDs que sumiram são podados; PID 3 volta como novo => 0.
	snap(&tr, 1400, map[int][2]uint64{1: {5, 160}})
	assert.Len(t, tr.prev, 1)
	got = snap(&tr, 1600, map[int][2]uint64{1: {5, 170}, 3: {7, 5000}})
	assert.Zero(t, got[3])
}

func TestTracker_AbortedSnapshotDoesNotInflateNext(t *testing.T) {
	var tr tracker
	snap(&tr, 1000, map[int][2]uint64{1: {5, 100}})

	// Leitura abortada: begin e percent, mas sem commit.
	tr.begin(1100)
	tr.percent(1, 5, 150)

	// Leitura seguinte, completa: Δproc 100 sobre Δtotal 200 => 50%
	// (e não 100%, que sairia de comparar 2 intervalos com 1 de total).
	got := snap(&tr, 1200, map[int][2]uint64{1: {5, 200}})
	assert.InDelta(t, 50.0, got[1], 1e-9)
}
