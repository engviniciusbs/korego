package github

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManifest_Fields(t *testing.T) {
	b := Manifest("https://painel.exemplo.com", "meu_host.exemplo.com")
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	assert.Equal(t, "KoreGO-meu-host-exemplo-com", m["name"])
	assert.Equal(t, "https://painel.exemplo.com", m["url"])
	assert.Equal(t, "https://painel.exemplo.com/api/github/callback", m["redirect_url"])
	assert.Equal(t, false, m["public"])
	assert.Equal(t, map[string]any{"url": "https://painel.exemplo.com/api/webhooks/github", "active": true}, m["hook_attributes"])
	assert.Equal(t, map[string]any{"contents": "read", "metadata": "read"}, m["default_permissions"])
	assert.Equal(t, []any{"push"}, m["default_events"])
	assert.Equal(t, b, Manifest("https://painel.exemplo.com", "meu_host.exemplo.com"))
}

func TestManifest_NameCut(t *testing.T) {
	b := Manifest("https://p", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	assert.Len(t, m["name"], 34)
}
