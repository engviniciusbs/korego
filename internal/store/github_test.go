package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/store"
)

func TestStoreGitHubApp_RoundTrip(t *testing.T) {
	db := store.OpenMemory(t)
	ctx := context.Background()

	_, err := db.GetGitHubApp(ctx)
	require.ErrorIs(t, err, store.ErrNotFound)

	app := store.GitHubApp{AppID: 42, Slug: "korego-x", Owner: "dono", HTMLURL: "https://github.com/apps/korego-x",
		PrivateKeyEnc: "pk", WebhookSecretEnc: "ws", CreatedAt: time.Unix(1700000000, 0)}
	require.NoError(t, db.SaveGitHubApp(ctx, &app))
	got, err := db.GetGitHubApp(ctx)
	require.NoError(t, err)
	assert.Equal(t, app.AppID, got.AppID)
	assert.Equal(t, "korego-x", got.Slug)
	assert.Equal(t, "pk", got.PrivateKeyEnc)
	assert.Equal(t, int64(1700000000), got.CreatedAt.Unix())

	app.Slug = "novo"
	require.NoError(t, db.SaveGitHubApp(ctx, &app))
	got, err = db.GetGitHubApp(ctx)
	require.NoError(t, err)
	assert.Equal(t, "novo", got.Slug)

	require.NoError(t, db.DeleteGitHubApp(ctx))
	_, err = db.GetGitHubApp(ctx)
	require.ErrorIs(t, err, store.ErrNotFound)
}
