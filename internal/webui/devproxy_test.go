package webui_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/engviniciusbs/korego/internal/webui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDevProxyForwardsGET(t *testing.T) {
	var gotHost, gotPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotPath = r.Host, r.URL.RequestURI()
		_, _ = w.Write([]byte("from-next"))
	}))
	defer up.Close()

	p, err := webui.NewDevProxy(up.URL)
	require.NoError(t, err)
	w := get(p, http.MethodGet, "/login/?a=1")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "from-next", w.Body.String())
	assert.Equal(t, "/login/?a=1", gotPath)
	assert.Equal(t, up.Listener.Addr().String(), gotHost)
}

func TestDevProxyInvalidTarget(t *testing.T) {
	_, err := webui.NewDevProxy("::nope")
	assert.Error(t, err)
}
