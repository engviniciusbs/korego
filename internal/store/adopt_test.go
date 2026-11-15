package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func adoptFixture(t *testing.T, db *DB) (App, Domain) {
	t.Helper()
	ctx := context.Background()
	p := &Project{Name: "proj-adot"}
	require.NoError(t, db.InsertProject(ctx, p))
	d := Domain{Hostname: "site.exemplo.com", WWWRedirect: "none", TLSMode: "auto", Routes: []DomainRoute{
		{Path: "/", TargetType: "static", Root: "/var/www/site/current", SPA: true},
		{Path: "/api/", Position: 1, TargetType: "address", Address: "127.0.0.1:5000"},
	}}
	require.NoError(t, db.InsertDomain(ctx, &d))
	a := App{ProjectID: p.ID, Slug: "site", SourceType: "git", Repo: "https://x/r.git", Branch: "main",
		BuildType: "static", HealthTimeoutS: 60, PublishRoot: "/var/www/site"}
	return a, d
}

func TestAdoptStatic_OneTransaction(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	a, d := adoptFixture(t, db)
	next := d
	next.Routes = []DomainRoute{{Path: "/", TargetType: "app"}, d.Routes[1]}
	dep := Deploy{Trigger: "adopt", Status: "running", CommitSHA: "1234567", ReleaseDir: "/var/www/site/releases/r"}

	require.NoError(t, db.AdoptStatic(ctx, &a, &dep, []Domain{next}))
	assert.Nil(t, next.Routes[0].AppID, "a entrada não muda")
	got, err := db.GetApp(ctx, a.ID)
	require.NoError(t, err)
	require.NotNil(t, got.CurrentDeployID)
	assert.Equal(t, dep.ID, *got.CurrentDeployID)
	gd, err := db.GetDeploy(ctx, dep.ID)
	require.NoError(t, err)
	assert.Equal(t, a.ID, gd.AppID)
	assert.Equal(t, "/var/www/site/releases/r", gd.ReleaseDir)
	assert.Equal(t, "running", gd.Status)
	assert.NotNil(t, gd.FinishedAt)
	dom, err := db.GetDomain(ctx, d.ID)
	require.NoError(t, err)
	require.NotNil(t, dom.Routes[0].AppID)
	assert.Equal(t, a.ID, *dom.Routes[0].AppID)
	assert.Equal(t, "address", dom.Routes[1].TargetType)
}

func TestAdoptStatic_DomainFailureLeavesNothing(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	a, d := adoptFixture(t, db)
	ghost := d
	ghost.ID, ghost.Hostname = 9999, "fantasma.exemplo.com"
	ghost.Routes = []DomainRoute{{Path: "/", TargetType: "app"}}
	ok := d
	ok.Routes = []DomainRoute{{Path: "/", TargetType: "app"}}
	dep := Deploy{Trigger: "adopt", Status: "running", ReleaseDir: "/var/www/site/releases/r"}

	err := db.AdoptStatic(ctx, &a, &dep, []Domain{ok, ghost})
	require.ErrorIs(t, err, ErrNotFound)
	apps, err := db.ListApps(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, apps, "nenhuma app")
	var n int
	require.NoError(t, db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM deploys`).Scan(&n))
	assert.Zero(t, n, "nenhum deploy")
	dom, err := db.GetDomain(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, "static", dom.Routes[0].TargetType, "o domínio que já tinha sido trocado volta")
	assert.Equal(t, "/var/www/site/current", dom.Routes[0].Root)
}
