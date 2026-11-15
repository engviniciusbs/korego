package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// mkApp cria um projeto próprio e uma app github com auto_deploy.
func mkApp(t *testing.T, db *DB, slug string) *App {
	t.Helper()
	ctx := context.Background()
	p := &Project{Name: "proj-" + slug}
	require.NoError(t, db.InsertProject(ctx, p))
	a := &App{ProjectID: p.ID, Slug: slug, SourceType: "github", Repo: "o/r", BuildType: "dockerfile",
		Branch: "main", DockerfilePath: "Dockerfile", ComposePath: "docker-compose.yml",
		InternalPort: 3000, HealthTimeoutS: 60, AutoDeploy: true}
	require.NoError(t, db.InsertApp(ctx, a))
	return a
}

func TestApps_SlugConflictAndByRepo(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	a := mkApp(t, db, "web")
	dup := *a
	dup.ID = 0
	require.ErrorIs(t, db.InsertApp(ctx, &dup), ErrConflict)

	mk := func(slug, src, branch string, auto bool) {
		x := &App{ProjectID: a.ProjectID, Slug: slug, SourceType: src, Repo: "o/r", BuildType: "dockerfile",
			Branch: branch, InternalPort: 80, AutoDeploy: auto}
		require.NoError(t, db.InsertApp(ctx, x))
	}
	mk("noauto", "github", "main", false)
	mk("other", "github", "dev", true)
	mk("git", "git", "main", true)

	got, err := db.AppsByRepo(ctx, "o/r", "main")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "web", got[0].Slug)

	// O full_name do GitHub pode vir com outra caixa; o branch não.
	got, err = db.AppsByRepo(ctx, "O/R", "main")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "web", got[0].Slug)
	got, err = db.AppsByRepo(ctx, "o/r", "Main")
	require.NoError(t, err)
	require.Len(t, got, 0)

	bySlug, err := db.GetAppBySlug(ctx, "web")
	require.NoError(t, err)
	require.Equal(t, a.ID, bySlug.ID)
	require.True(t, bySlug.AutoDeploy)
	require.Nil(t, bySlug.CurrentDeployID)
	_, err = db.GetAppBySlug(ctx, "nope")
	require.ErrorIs(t, err, ErrNotFound)

	all, err := db.ListApps(ctx, nil)
	require.NoError(t, err)
	require.Len(t, all, 4)
	one, err := db.ListApps(ctx, &a.ProjectID)
	require.NoError(t, err)
	require.Len(t, one, 4)
	other := mkApp(t, db, "solo")
	one, err = db.ListApps(ctx, &other.ProjectID)
	require.NoError(t, err)
	require.Len(t, one, 1)

	a.InternalPort = 8080
	require.NoError(t, db.UpdateApp(ctx, a))
	g, err := db.GetApp(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, 8080, g.InternalPort)
	require.ErrorIs(t, db.UpdateApp(ctx, &App{ID: 999, ProjectID: a.ProjectID, Slug: "z"}), ErrNotFound)
}

func TestApps_DeleteWithDomainConflict(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	a := mkApp(t, db, "web")
	d := &Domain{Hostname: "x.exemplo.com", TargetType: "app", AppID: &a.ID, WWWRedirect: "none"}
	require.NoError(t, db.InsertDomain(ctx, d))
	require.ErrorIs(t, db.DeleteApp(ctx, a.ID), ErrConflict)
	require.NoError(t, db.DeleteDomain(ctx, d.ID))
	require.NoError(t, db.DeleteApp(ctx, a.ID))
	require.ErrorIs(t, db.DeleteApp(ctx, a.ID), ErrNotFound)
}

func TestAppEnv_SetRemoveNames(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	a := mkApp(t, db, "web")
	require.NoError(t, db.SetAppEnv(ctx, a.ID, map[string]string{"B": "eb", "A": "ea"}, nil))
	names, err := db.AppEnvNames(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"A", "B"}, names)

	require.NoError(t, db.SetAppEnv(ctx, a.ID, map[string]string{"B": "eb2"}, []string{"A"}))
	names, err = db.AppEnvNames(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"B"}, names)
	enc, err := db.AppEnvEncrypted(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"B": "eb2"}, enc)

	// apagar a app leva as variáveis junto (CASCADE)
	require.NoError(t, db.DeleteApp(ctx, a.ID))
	enc, err = db.AppEnvEncrypted(ctx, a.ID)
	require.NoError(t, err)
	require.Empty(t, enc)
}

func TestUsedHostPorts(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	a := mkApp(t, db, "web")
	a.HostPort = 20001
	require.NoError(t, db.UpdateApp(ctx, a))
	b := mkApp(t, db, "api")

	for _, c := range []struct {
		status string
		port   int
	}{{"starting", 20002}, {"running", 20003}, {"failed", 20004}, {"superseded", 20005}, {"running", 0}} {
		require.NoError(t, db.InsertDeploy(ctx, &Deploy{AppID: b.ID, Trigger: "manual", Status: c.status, HostPort: c.port}))
	}
	got, err := db.UsedHostPorts(ctx)
	require.NoError(t, err)
	require.Equal(t, map[int]bool{20001: true, 20002: true, 20003: true}, got)
}

func TestApps_UpdateAppConfigKeepsPortAndCurrent(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	a := mkApp(t, db, "cfg")
	m := &Deploy{AppID: a.ID, Trigger: "manual", Status: "running"}
	require.NoError(t, db.InsertDeploy(ctx, m))
	require.NoError(t, db.SetCurrentDeploy(ctx, a.ID, m.ID))
	require.NoError(t, db.SetAppHostPort(ctx, a.ID, 20123))

	stale := *a // cópia antiga: host_port 0 e sem deploy atual
	stale.InternalPort = 9090
	require.NoError(t, db.UpdateAppConfig(ctx, &stale))
	g, err := db.GetApp(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, 9090, g.InternalPort)
	require.Equal(t, 20123, g.HostPort)
	require.NotNil(t, g.CurrentDeployID)
	require.Equal(t, m.ID, *g.CurrentDeployID)
	require.ErrorIs(t, db.UpdateAppConfig(ctx, &App{ID: 999, ProjectID: a.ProjectID, Slug: "z"}), ErrNotFound)
}

func TestApps_SetAppHostPortOnlyPort(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	a := mkApp(t, db, "port")
	require.NoError(t, db.SetAppHostPort(ctx, a.ID, 20001))
	g, err := db.GetApp(ctx, a.ID)
	require.NoError(t, err)
	want := *a
	want.HostPort, want.UpdatedAt = 20001, g.UpdatedAt
	require.Equal(t, want, g)
	require.NoError(t, db.SetAppHostPort(ctx, a.ID, 0))
	require.ErrorIs(t, db.SetAppHostPort(ctx, 999, 1), ErrNotFound)
}

func TestApps_StaticAndLocalFieldsRoundTrip(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	p := &Project{Name: "sites"}
	require.NoError(t, db.InsertProject(ctx, p))

	st := &App{ProjectID: p.ID, Slug: "blog", SourceType: "github", Repo: "o/blog", BuildType: "static",
		Branch: "main", PublishRoot: "/var/www/blog", StaticBuildCmd: "npm run build",
		StaticOutputDir: "dist", StaticBaseDir: "web", StaticImage: "node:22", StaticSPA: true,
		StaticImmutablePrefix: "/assets/"}
	require.NoError(t, db.InsertApp(ctx, st))
	loc := &App{ProjectID: p.ID, Slug: "loja", SourceType: "local", BuildType: "compose-local",
		ComposeProject: "loja", UpdateMode: "script", UpdateScript: "/srv/loja/update.sh"}
	require.NoError(t, db.InsertApp(ctx, loc))

	got, err := db.GetApp(ctx, st.ID)
	require.NoError(t, err)
	require.Equal(t, "/var/www/blog", got.PublishRoot)
	require.Equal(t, "npm run build", got.StaticBuildCmd)
	require.Equal(t, "dist", got.StaticOutputDir)
	require.Equal(t, "web", got.StaticBaseDir)
	require.Equal(t, "node:22", got.StaticImage)
	require.True(t, got.StaticSPA)
	require.Equal(t, "/assets/", got.StaticImmutablePrefix)
	require.Equal(t, 0, got.InternalPort)

	got, err = db.GetApp(ctx, loc.ID)
	require.NoError(t, err)
	require.Equal(t, "loja", got.ComposeProject)
	require.Equal(t, "script", got.UpdateMode)
	require.Equal(t, "/srv/loja/update.sh", got.UpdateScript)
	require.False(t, got.StaticSPA)

	// Atualização regrava os campos novos.
	st.StaticSPA, st.StaticOutputDir, st.UpdateMode = false, "build", "pull"
	require.NoError(t, db.UpdateApp(ctx, st))
	got, _ = db.GetApp(ctx, st.ID)
	require.False(t, got.StaticSPA)
	require.Equal(t, "build", got.StaticOutputDir)
	require.Equal(t, "pull", got.UpdateMode)
	st.StaticOutputDir = "out"
	require.NoError(t, db.UpdateAppConfig(ctx, st))
	got, _ = db.GetApp(ctx, st.ID)
	require.Equal(t, "out", got.StaticOutputDir)
}

func TestAppByComposeProject(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	a := mkApp(t, db, "loja")
	a.ComposeProject = "loja-compose"
	require.NoError(t, db.UpdateApp(ctx, a))
	got, err := db.AppByComposeProject(ctx, "loja-compose")
	require.NoError(t, err)
	require.Equal(t, a.ID, got.ID)
	_, err = db.AppByComposeProject(ctx, "nada")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAppByPublishRoot(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	a := mkApp(t, db, "site")
	a.PublishRoot = "/var/www/site"
	require.NoError(t, db.UpdateApp(ctx, a))
	got, err := db.AppByPublishRoot(ctx, "/var/www/site")
	require.NoError(t, err)
	require.Equal(t, a.ID, got.ID)
	_, err = db.AppByPublishRoot(ctx, "/outro")
	require.ErrorIs(t, err, ErrNotFound)
}
