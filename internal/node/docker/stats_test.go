package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statsStream monta um stream JSON de amostras como o do Docker.
func statsStream(t *testing.T, samples ...container.StatsResponse) *bytes.Buffer {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	for _, s := range samples {
		require.NoError(t, enc.Encode(s))
	}
	return &b
}

func TestDecodeStats_StopsOnZeroRead(t *testing.T) {
	read := time.Unix(1_762_430_400, 0)
	live := container.StatsResponse{Read: read, MemoryStats: container.MemoryStats{Usage: 10, Limit: 100}}
	// Container parado: o Docker continua mandando amostras com read zerado.
	stopped := container.StatsResponse{}
	var got []Stats
	err := decodeStats(context.Background(), statsStream(t, live, live, stopped, live), func(s Stats) { got = append(got, s) })
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestDecodeStats_EOFEndsStream(t *testing.T) {
	live := container.StatsResponse{Read: time.Unix(1_762_430_400, 0)}
	var n int
	require.NoError(t, decodeStats(context.Background(), statsStream(t, live), func(Stats) { n++ }))
	assert.Equal(t, 1, n)
}
