package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProjects_CRUD(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	p := &Project{Name: "loja", Description: "d"}
	require.NoError(t, db.InsertProject(ctx, p))
	require.NotZero(t, p.ID)
	require.ErrorIs(t, db.InsertProject(ctx, &Project{Name: "loja"}), ErrConflict)

	p.Description = "novo"
	require.NoError(t, db.UpdateProject(ctx, p))
	got, err := db.GetProject(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, "novo", got.Description)
	require.ErrorIs(t, db.UpdateProject(ctx, &Project{ID: 999, Name: "x"}), ErrNotFound)
	_, err = db.GetProject(ctx, 999)
	require.ErrorIs(t, err, ErrNotFound)

	list, err := db.ListProjects(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, db.DeleteProject(ctx, p.ID))
	require.ErrorIs(t, db.DeleteProject(ctx, p.ID), ErrNotFound)
}

func TestProjects_DeleteWithAppsConflict(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	app := mkApp(t, db, "a1")
	require.ErrorIs(t, db.DeleteProject(ctx, app.ProjectID), ErrConflict)
	require.NoError(t, db.DeleteApp(ctx, app.ID))
	require.NoError(t, db.DeleteProject(ctx, app.ProjectID))
}
