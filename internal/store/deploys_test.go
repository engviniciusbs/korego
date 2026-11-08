package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func mkDeploy(t *testing.T, db *DB, appID int64, status, image string) *Deploy {
	t.Helper()
	d := &Deploy{AppID: appID, Trigger: "manual", Status: status, Image: image}
	require.NoError(t, db.InsertDeploy(context.Background(), d))
	return d
}

func TestDeploys_StatusTimestamps(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	a := mkApp(t, db, "web")
	d := mkDeploy(t, db, a.ID, "queued", "")
	require.NotZero(t, d.ID)

	t0 := time.Unix(1_700_000_000, 0)
	require.NoError(t, db.UpdateDeployStatus(ctx, d.ID, "building", "", t0))
	got, err := db.GetDeploy(ctx, d.ID)
	require.NoError(t, err)
	require.Equal(t, "building", got.Status)
	require.NotNil(t, got.StartedAt)
	require.Equal(t, t0.Unix(), got.StartedAt.Unix())
	require.Nil(t, got.FinishedAt)

	// started_at só é definido uma vez
	require.NoError(t, db.UpdateDeployStatus(ctx, d.ID, "building", "", t0.Add(time.Minute)))
	got, _ = db.GetDeploy(ctx, d.ID)
	require.Equal(t, t0.Unix(), got.StartedAt.Unix())

	t1 := t0.Add(time.Hour)
	require.NoError(t, db.UpdateDeployStatus(ctx, d.ID, "failed", "erro de build", t1))
	got, _ = db.GetDeploy(ctx, d.ID)
	require.Equal(t, "erro de build", got.Reason)
	require.NotNil(t, got.FinishedAt)
	require.Equal(t, t1.Unix(), got.FinishedAt.Unix())

	require.ErrorIs(t, db.UpdateDeployStatus(ctx, 999, "failed", "", t1), ErrNotFound)
	_, err = db.GetDeploy(ctx, 999)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestDeploys_ResultCurrentAndList(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	a := mkApp(t, db, "web")
	d1 := mkDeploy(t, db, a.ID, "queued", "")
	d2 := mkDeploy(t, db, a.ID, "queued", "")
	require.NoError(t, db.SetDeployResult(ctx, d1.ID, "img:1", 20001, "cid", "sha1", "msg"))
	got, _ := db.GetDeploy(ctx, d1.ID)
	require.Equal(t, "img:1", got.Image)
	require.Equal(t, 20001, got.HostPort)
	require.Equal(t, "cid", got.ContainerID)
	require.Equal(t, "sha1", got.CommitSHA)
	require.Equal(t, "msg", got.CommitMessage)
	require.ErrorIs(t, db.SetDeployResult(ctx, 999, "", 0, "", "", ""), ErrNotFound)

	require.NoError(t, db.SetCurrentDeploy(ctx, a.ID, d1.ID))
	ga, _ := db.GetApp(ctx, a.ID)
	require.NotNil(t, ga.CurrentDeployID)
	require.Equal(t, d1.ID, *ga.CurrentDeployID)
	require.ErrorIs(t, db.SetCurrentDeploy(ctx, 999, d1.ID), ErrNotFound)

	list, err := db.ListDeploys(ctx, a.ID, 10)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, d2.ID, list[0].ID)
	list, _ = db.ListDeploys(ctx, a.ID, 1)
	require.Len(t, list, 1)

	q, err := db.QueuedDeploys(ctx)
	require.NoError(t, err)
	require.Len(t, q, 2)
	require.Equal(t, d1.ID, q[0].ID) // mais antigo primeiro
}

func TestDeploys_MarkInterrupted(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	a := mkApp(t, db, "web")
	b := mkDeploy(t, db, a.ID, "building", "")
	s := mkDeploy(t, db, a.ID, "starting", "")
	q := mkDeploy(t, db, a.ID, "queued", "")
	r := mkDeploy(t, db, a.ID, "running", "")

	now := time.Unix(1_700_000_000, 0)
	got, err := db.MarkInterrupted(ctx, now)
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, d := range got {
		require.Equal(t, "failed", d.Status)
		require.Equal(t, "interrompido", d.Reason)
		require.NotNil(t, d.FinishedAt)
	}
	for _, id := range []int64{b.ID, s.ID} {
		d, _ := db.GetDeploy(ctx, id)
		require.Equal(t, "failed", d.Status)
		require.Equal(t, "interrompido", d.Reason)
	}
	dq, _ := db.GetDeploy(ctx, q.ID)
	require.Equal(t, "queued", dq.Status)
	dr, _ := db.GetDeploy(ctx, r.ID)
	require.Equal(t, "running", dr.Status)

	again, err := db.MarkInterrupted(ctx, now)
	require.NoError(t, err)
	require.Empty(t, again)
}

func TestDeploys_LatestImagesDistinct(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	a := mkApp(t, db, "web")
	mkDeploy(t, db, a.ID, "superseded", "img:1")
	mkDeploy(t, db, a.ID, "failed", "img:fail")
	mkDeploy(t, db, a.ID, "superseded", "img:2")
	mkDeploy(t, db, a.ID, "superseded", "img:2")
	mkDeploy(t, db, a.ID, "superseded", "")
	mkDeploy(t, db, a.ID, "superseded", "img:3")
	mkDeploy(t, db, a.ID, "running", "img:4")

	got, err := db.LatestImages(ctx, a.ID, 3)
	require.NoError(t, err)
	require.Equal(t, []string{"img:4", "img:3", "img:2"}, got)
	got, _ = db.LatestImages(ctx, a.ID, 10)
	require.Equal(t, []string{"img:4", "img:3", "img:2", "img:1"}, got)
}
