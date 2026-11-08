package node_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/node"
)

func TestLocal_Info(t *testing.T) {
	info, err := node.NewLocal("", "").Info(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, info.Hostname)
	assert.GreaterOrEqual(t, info.CPUs, 1)
	assert.Greater(t, info.MemoryTotalBytes, uint64(0))
}

func TestLocal_Proxy(t *testing.T) {
	assert.NotNil(t, node.NewLocal("", "").Proxy())
}
