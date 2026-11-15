package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/domains"
	"github.com/engviniciusbs/korego/internal/store"
)

func TestStatRoot_FollowsCurrentLink(t *testing.T) {
	dir := t.TempDir()
	rel := filepath.Join(dir, "releases", "20261115133041-9faa1c9")
	require.NoError(t, os.MkdirAll(rel, 0o755))
	require.NoError(t, os.Symlink(rel, filepath.Join(dir, "current")))

	got := statRoot("/", filepath.Join(dir, "current"))
	require.True(t, got.Exists)
	require.Equal(t, "/", got.Path)
	require.Equal(t, "20261115133041-9faa1c9", filepath.Base(got.Resolved))
	require.NotNil(t, got.ModifiedAt)

	missing := statRoot("/", filepath.Join(dir, "nada"))
	require.False(t, missing.Exists)
	require.Nil(t, missing.ModifiedAt)
	require.Empty(t, missing.Resolved)
}

func TestToDomainDTO_StaticRootsOnlyForStaticRoutes(t *testing.T) {
	dir := t.TempDir()
	v := domains.DomainView{Domain: store.Domain{Hostname: "x.com", Routes: []store.DomainRoute{
		{Path: "/api/", TargetType: "address", Address: "127.0.0.1:1"},
		{Path: "/", TargetType: "static", Root: dir},
	}}}
	out := toDomainDTO(v)
	require.Len(t, out.StaticRoots, 1)
	require.True(t, out.StaticRoots[0].Exists)

	empty := toDomainDTO(domains.DomainView{Domain: store.Domain{Hostname: "y.com"}})
	require.NotNil(t, empty.StaticRoots)
}
