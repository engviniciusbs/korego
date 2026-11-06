package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocal_Sample(t *testing.T) {
	l := NewLocal()
	ctx := context.Background()

	first, err := l.Sample(ctx)
	require.NoError(t, err)
	assert.Greater(t, first.MemTotalBytes, uint64(0))
	assert.GreaterOrEqual(t, len(first.Disks), 1)

	time.Sleep(50 * time.Millisecond)
	second, err := l.Sample(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, second.CPUPercent, 0.0)
	assert.LessOrEqual(t, second.CPUPercent, 100.0)
	assert.GreaterOrEqual(t, second.NetRxBytesPerSec, 0.0)
	assert.GreaterOrEqual(t, second.NetTxBytesPerSec, 0.0)
	assert.True(t, second.Time.After(first.Time))
}

func TestLocal_Sample_NetDeltas(t *testing.T) {
	clock := time.Unix(1000, 0)
	counters := map[string]netCounter{}
	l := NewLocal()
	l.now = func() time.Time { return clock }
	l.cpuFn = func(context.Context) (cpu.TimesStat, error) { return cpu.TimesStat{}, nil }
	l.netFn = func(context.Context) (map[string]netCounter, error) { return counters, nil }
	step := func(next map[string]netCounter) Sample {
		counters = next
		clock = clock.Add(2 * time.Second)
		s, err := l.Sample(context.Background())
		require.NoError(t, err)
		return s
	}
	ctx := context.Background()

	counters = map[string]netCounter{"eth0": {1000, 100}, "eth1": {5000, 500}}
	first, err := l.Sample(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0.0, first.NetRxBytesPerSec)
	assert.Equal(t, 0.0, first.NetTxBytesPerSec)

	// eth0 recuou (reset): contribui 0; eth1 soma o delta real.
	s := step(map[string]netCounter{"eth0": {10, 5}, "eth1": {7000, 900}})
	assert.Equal(t, 1000.0, s.NetRxBytesPerSec)
	assert.Equal(t, 200.0, s.NetTxBytesPerSec)

	// Interface nova com contador enorme: sem pico.
	s = step(map[string]netCounter{"eth0": {210, 105}, "eth1": {9000, 900}, "wg0": {1 << 40, 1 << 40}})
	assert.Equal(t, 1100.0, s.NetRxBytesPerSec)
	assert.Equal(t, 50.0, s.NetTxBytesPerSec)

	// Interface sumiu: as demais continuam contando.
	s = step(map[string]netCounter{"eth0": {410, 105}, "wg0": {1<<40 + 400, 1<<40 + 20}})
	assert.Equal(t, 300.0, s.NetRxBytesPerSec)
	assert.Equal(t, 10.0, s.NetTxBytesPerSec)
}
