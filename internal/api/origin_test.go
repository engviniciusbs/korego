package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPanelOrigin_NilDomains(t *testing.T) {
	o, ok, err := panelOrigin(context.Background(), nil)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, o)
}
