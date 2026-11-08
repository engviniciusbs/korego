package deploy

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/docker/dockertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanup_KeepsThreeAndInUse(t *testing.T) {
	var removed []string
	var repo string
	d := &dockertest.Fake{
		ImagesFn: func(_ context.Context, r string) ([]docker.Image, error) {
			repo = r
			return []docker.Image{
				{Ref: "korego/blog:a1"}, {Ref: "korego/blog:a2"}, {Ref: "korego/blog:a3"},
				{Ref: "korego/blog:old1"}, {Ref: "korego/blog:inuse"}, {Ref: "korego/blog:old2"},
			}, nil
		},
		RemoveImageFn: func(_ context.Context, ref string) error {
			removed = append(removed, ref)
			if ref == "korego/blog:inuse" {
				return docker.ErrConflict
			}
			return nil
		},
	}
	var out bytes.Buffer
	err := Cleanup(context.Background(), d, appFor("blog"), []string{"korego/blog:a1", "korego/blog:a2", "korego/blog:a3"}, &out)
	require.NoError(t, err)
	assert.Equal(t, "korego/blog", repo)
	assert.Equal(t, []string{"korego/blog:old1", "korego/blog:inuse", "korego/blog:old2"}, removed)
	assert.Contains(t, out.String(), "korego/blog:inuse")
}

func TestCleanup_JoinsOtherErrors(t *testing.T) {
	boom := errors.New("boom")
	var n int
	d := &dockertest.Fake{
		ImagesFn: func(context.Context, string) ([]docker.Image, error) {
			return []docker.Image{{Ref: "korego/blog:x"}, {Ref: "korego/blog:y"}}, nil
		},
		RemoveImageFn: func(context.Context, string) error { n++; return boom },
	}
	err := Cleanup(context.Background(), d, appFor("blog"), nil, &bytes.Buffer{})
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, 2, n)
}

func TestCleanup_ListError(t *testing.T) {
	d := &dockertest.Fake{}
	assert.Error(t, Cleanup(context.Background(), d, appFor("blog"), nil, &bytes.Buffer{}))
}
