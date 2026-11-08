package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSettings_RoundTrip(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()

	_, ok, err := db.GetSetting(ctx, "panel.public_port")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, db.SetSetting(ctx, "panel.public_port", "false"))
	v, ok, err := db.GetSetting(ctx, "panel.public_port")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "false", v)

	require.NoError(t, db.SetSetting(ctx, "panel.public_port", "true"))
	v, _, err = db.GetSetting(ctx, "panel.public_port")
	require.NoError(t, err)
	require.Equal(t, "true", v)

	require.NoError(t, db.DeleteSetting(ctx, "panel.public_port"))
	_, ok, err = db.GetSetting(ctx, "panel.public_port")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, db.DeleteSetting(ctx, "panel.public_port"))
}
