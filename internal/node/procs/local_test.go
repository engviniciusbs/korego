package procs

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocal_Real(t *testing.T) {
	l := NewLocal()
	ctx := context.Background()
	_, err := l.Snapshot(ctx)
	require.NoError(t, err)
	ps, err := l.Snapshot(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, ps)
	found := false
	for _, p := range ps {
		assert.GreaterOrEqual(t, p.CPUPercent, 0.0)
		assert.LessOrEqual(t, p.CPUPercent, 100.0)
		if p.PID == os.Getpid() {
			found = true
		}
	}
	assert.True(t, found, "o próprio processo deve aparecer")
}
