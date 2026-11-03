package update_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/update"
)

func TestAvailable(t *testing.T) {
	assert.True(t, update.Available("v0.1.0", "v0.2.0"))
	assert.True(t, update.Available("0.1.0", "0.2.0"))
	assert.False(t, update.Available("0.2.0", "v0.2.0"))
	assert.False(t, update.Available("0.3.0", "0.2.0"))
	assert.False(t, update.Available("dev", "0.2.0"))
	assert.False(t, update.Available("0.1.0", "lixo"))
}

func TestLatest_PicksArchAsset(t *testing.T) {
	var gotAccept, gotUA, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept, gotUA, gotPath = r.Header.Get("Accept"), r.Header.Get("User-Agent"), r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v0.2.0",
			"assets": []map[string]string{
				{"name": "korego_0.2.0_linux_amd64.tar.gz", "browser_download_url": "https://exemplo.com/amd64"},
				{"name": "korego_0.2.0_linux_arm64.tar.gz", "browser_download_url": "https://exemplo.com/arm64"},
				{"name": "checksums.txt", "browser_download_url": "https://exemplo.com/sums"},
			},
		})
	}))
	defer srv.Close()

	c := &update.Checker{BaseURL: srv.URL, Repo: "o/r"}
	rel, err := c.Latest(context.Background(), "arm64")
	require.NoError(t, err)
	assert.Equal(t, "/repos/o/r/releases/latest", gotPath)
	assert.Equal(t, "application/vnd.github+json", gotAccept)
	assert.Contains(t, gotUA, "korego/")
	assert.Equal(t, update.Release{
		Version: "v0.2.0", TarballURL: "https://exemplo.com/arm64", ChecksumsURL: "https://exemplo.com/sums",
	}, rel)
}

func TestLatest_Errors(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"tag_name":"v1.0.0","assets":[]}`))
	}))
	defer srv.Close()
	c := &update.Checker{BaseURL: srv.URL}
	_, err := c.Latest(context.Background(), "amd64")
	require.Error(t, err, "sem assets")
	status = http.StatusNotFound
	_, err = c.Latest(context.Background(), "amd64")
	require.Error(t, err, "status não-200")
}
