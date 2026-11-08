package deploy

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/engviniciusbs/korego/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func composeApp() store.App {
	return store.App{Slug: "blog", ComposePath: "deploy/compose.yml", ComposeService: "web", InternalPort: 3000}
}

func TestComposeOverride_YAML(t *testing.T) {
	b, err := ComposeOverride(composeApp(), 20005)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, yaml.Unmarshal(b, &got))
	want := map[string]any{"services": map[string]any{"web": map[string]any{
		"ports":  []any{"127.0.0.1:20005:3000"},
		"labels": map[string]any{"korego.managed": "true", "korego.app": "blog"},
	}}}
	assert.Equal(t, want, got)
	// "true" tem de sair como string, não booleano
	assert.Contains(t, string(b), `"true"`)
}

func TestComposeOverride_Validates(t *testing.T) {
	a := composeApp()
	a.ComposeService = "web; rm"
	_, err := ComposeOverride(a, 20005)
	assert.Error(t, err)
	a = composeApp()
	a.InternalPort = 0
	_, err = ComposeOverride(a, 20005)
	assert.Error(t, err)
	_, err = ComposeOverride(composeApp(), 70000)
	assert.Error(t, err)
}

func TestComposeUp_Args(t *testing.T) {
	r := &fakeRunner{}
	var out bytes.Buffer
	require.NoError(t, ComposeUp(context.Background(), r, composeApp(), "/work/src", "/work/override.yml", &out))
	require.Len(t, r.cmds, 1)
	c := r.cmds[0]
	assert.Equal(t, "docker", c.Name)
	assert.Equal(t, []string{"compose", "-p", "korego-blog", "-f", filepath.Join("/work/src", "deploy", "compose.yml"),
		"-f", "/work/override.yml", "up", "-d", "--remove-orphans"}, c.Args)
}

func TestComposeUp_RejectsBadInput(t *testing.T) {
	r := &fakeRunner{}
	a := composeApp()
	a.ComposePath = "../x.yml"
	assert.Error(t, ComposeUp(context.Background(), r, a, "/w", "/o.yml", &bytes.Buffer{}))
	a = composeApp()
	a.Slug = "Bad Slug"
	assert.Error(t, ComposeUp(context.Background(), r, a, "/w", "/o.yml", &bytes.Buffer{}))
	assert.Empty(t, r.cmds)
}
