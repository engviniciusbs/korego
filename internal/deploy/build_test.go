package deploy

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func storeApp(slug string) store.App { return store.App{Slug: slug} }

// fakeDocker implementa só o Pull; os demais métodos entram em pânico se usados.
type fakeDocker struct{ docker.Client }

func (*fakeDocker) Pull(_ context.Context, _ string, fn func(string)) error {
	fn("layer: pulling")
	return nil
}

func TestBuild_DockerfileArgs(t *testing.T) {
	f := &fakeRunner{}
	app := store.App{Slug: "blog", BuildType: "dockerfile", DockerfilePath: "docker/Dockerfile"}
	require.NoError(t, Build(context.Background(), f, app, "/b/1", "korego/blog:abc", &bytes.Buffer{}))
	require.Len(t, f.cmds, 1)
	assert.Equal(t, "docker", f.cmds[0].Name)
	assert.Equal(t, []string{"build", "-t", "korego/blog:abc", "-f", filepath.Join("/b/1", "docker/Dockerfile"), "/b/1"}, f.cmds[0].Args)
}

func TestBuild_RejectsBadPath(t *testing.T) {
	f := &fakeRunner{}
	app := store.App{Slug: "blog", BuildType: "dockerfile", DockerfilePath: "../x"}
	assert.Error(t, Build(context.Background(), f, app, "/b/1", "t", &bytes.Buffer{}))
	assert.Empty(t, f.cmds)
}

func TestBuild_RailpackMissing(t *testing.T) {
	old := LookPath
	defer func() { LookPath = old }()
	LookPath = func(string) (string, error) { return "", errors.New("nope") }
	f := &fakeRunner{}
	err := Build(context.Background(), f, store.App{Slug: "a", BuildType: "railpack"}, "/b/1", "t", &bytes.Buffer{})
	assert.ErrorIs(t, err, ErrRailpackMissing)
	assert.Empty(t, f.cmds)
}

func TestBuild_RailpackArgs(t *testing.T) {
	old := LookPath
	defer func() { LookPath = old }()
	LookPath = func(s string) (string, error) { return s, nil }
	f := &fakeRunner{}
	require.NoError(t, Build(context.Background(), f, store.App{Slug: "a", BuildType: "railpack"}, "/b/1", "korego/a:abc", &bytes.Buffer{}))
	require.Len(t, f.cmds, 2)
	plan := filepath.Clean("/b/1") + ".railpack-plan.json"
	assert.Equal(t, "railpack", f.cmds[0].Name)
	assert.Equal(t, []string{"prepare", "/b/1", "--plan-out", plan}, f.cmds[0].Args)
	assert.Equal(t, "docker", f.cmds[1].Name)
	assert.Equal(t, []string{"buildx", "build", "--build-arg", "BUILDKIT_SYNTAX=ghcr.io/railwayapp/railpack-frontend",
		"-f", plan, "-t", "korego/a:abc", "--load", "/b/1"}, f.cmds[1].Args)
}

func TestBuild_ComposeArgs(t *testing.T) {
	f := &fakeRunner{}
	app := store.App{Slug: "shop", BuildType: "compose", ComposePath: "compose.yml"}
	require.NoError(t, Build(context.Background(), f, app, "/b/1", "ignorado", &bytes.Buffer{}))
	require.Len(t, f.cmds, 1)
	assert.Equal(t, []string{"compose", "-p", "korego-shop", "-f", filepath.Join("/b/1", "compose.yml"), "build"}, f.cmds[0].Args)
}

func TestBuild_ImageNotHandled(t *testing.T) {
	f := &fakeRunner{}
	assert.Error(t, Build(context.Background(), f, store.App{Slug: "a", BuildType: "image"}, "/b", "t", &bytes.Buffer{}))
	assert.Empty(t, f.cmds)
}
