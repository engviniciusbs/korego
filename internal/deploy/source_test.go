package deploy

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetch_ArgsExact(t *testing.T) {
	f := &fakeRunner{outputs: []string{"", "abc1234def5678\n", "corrige bug\n"}}
	sha, msg, err := Fetch(context.Background(), f, "/b/1", Source{URL: "https://github.com/o/r.git", Branch: "main"}, &bytes.Buffer{})
	require.NoError(t, err)
	assert.Equal(t, "abc1234def5678", sha)
	assert.Equal(t, "corrige bug", msg)
	require.Len(t, f.cmds, 3)
	assert.Equal(t, "git", f.cmds[0].Name)
	assert.Equal(t, []string{"clone", "--depth", "1", "--branch", "main", "--single-branch", "https://github.com/o/r.git", "/b/1"}, f.cmds[0].Args)
	assert.Empty(t, f.cmds[0].Env)
	assert.Equal(t, []string{"-C", "/b/1", "rev-parse", "HEAD"}, f.cmds[1].Args)
	assert.Equal(t, []string{"-C", "/b/1", "log", "-1", "--format=%s"}, f.cmds[2].Args)
}

func TestFetch_TokenOnlyInEnv(t *testing.T) {
	f := &fakeRunner{outputs: []string{"", "sha\n", "msg\n"}}
	tok := "ghs_SEGREDO"
	_, _, err := Fetch(context.Background(), f, "/b/1", Source{URL: "https://github.com/o/r.git", Branch: "main", Token: tok}, &bytes.Buffer{})
	require.NoError(t, err)
	b64 := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + tok))
	for _, c := range f.cmds {
		for _, a := range c.Args {
			assert.NotContains(t, a, tok)
			assert.NotContains(t, a, "x-access-token")
			assert.NotContains(t, a, b64)
		}
	}
	assert.Equal(t, []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.https://github.com/.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic " + b64}, f.cmds[0].Env)
}

func TestFetch_RejectsBadInput(t *testing.T) {
	f := &fakeRunner{}
	_, _, err := Fetch(context.Background(), f, "/b", Source{URL: "http://x.com/a", Branch: "main"}, &bytes.Buffer{})
	assert.Error(t, err)
	_, _, err = Fetch(context.Background(), f, "/b", Source{URL: "https://x.com/a", Branch: "-x"}, &bytes.Buffer{})
	assert.Error(t, err)
	assert.Empty(t, f.cmds)
}

func TestFetch_CloneError(t *testing.T) {
	f := &fakeRunner{errs: map[int]error{0: errors.New("boom")}}
	_, _, err := Fetch(context.Background(), f, "/b", Source{URL: "https://x.com/a", Branch: "main"}, &bytes.Buffer{})
	assert.Error(t, err)
	assert.Len(t, f.cmds, 1)
}

func TestImageForApp(t *testing.T) {
	assert.Equal(t, "korego/blog:abc1234", ImageForApp(storeApp("blog"), "abc1234def", ""))
	assert.Equal(t, "korego/blog:0123456", ImageForApp(storeApp("blog"), "", "sha256:0123456789abcdef"))
	assert.Equal(t, "korego/blog:abc", ImageForApp(storeApp("blog"), "abc", ""))
}

func TestPullAndTag(t *testing.T) {
	f := &fakeRunner{}
	d := &fakeDocker{}
	var out bytes.Buffer
	require.NoError(t, PullAndTag(context.Background(), d, f, "nginx:1-alpine", "korego/web:0123456", &out))
	assert.Contains(t, out.String(), "layer: pulling")
	require.Len(t, f.cmds, 1)
	assert.Equal(t, "docker", f.cmds[0].Name)
	assert.Equal(t, []string{"tag", "nginx:1-alpine", "korego/web:0123456"}, f.cmds[0].Args)
}

func TestPullImage_ReturnsID(t *testing.T) {
	f := &fakeRunner{outputs: []string{"sha256:0123456789abcdef\n"}}
	id, err := PullImage(context.Background(), &fakeDocker{}, f, "nginx", &bytes.Buffer{})
	require.NoError(t, err)
	assert.Equal(t, "sha256:0123456789abcdef", id)
	assert.Equal(t, []string{"image", "inspect", "--format", "{{.Id}}", "nginx"}, f.cmds[0].Args)
}

func TestTokenSecrets(t *testing.T) {
	assert.Nil(t, TokenSecrets(""))
	b64 := base64.StdEncoding.EncodeToString([]byte("x-access-token:ghs_X"))
	assert.Equal(t, []string{"ghs_X", b64}, TokenSecrets("ghs_X"))
}
