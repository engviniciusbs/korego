package metrics

import (
	"context"
	"testing"
	"time"

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
