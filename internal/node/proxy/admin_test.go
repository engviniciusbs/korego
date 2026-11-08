package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdmin_LoadAndConfig(t *testing.T) {
	var gotBody, gotType, gotMethod, gotPath string
	stored := `{"apps":{}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/load":
			gotBody, gotType, gotMethod, gotPath = string(b), r.Header.Get("Content-Type"), r.Method, r.URL.Path
			if strings.Contains(gotBody, "ruim") {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":"loading config: json: ruim"}`)
				return
			}
			stored = gotBody
		case r.Method == http.MethodGet && r.URL.Path == "/config/":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, stored+"\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &AdminClient{BaseURL: srv.URL}
	ctx := context.Background()

	require.NoError(t, c.Load(ctx, []byte(`{"admin":{"listen":"127.0.0.1:2019"}}`)))
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/load", gotPath)
	assert.Equal(t, "application/json", gotType)
	assert.Equal(t, `{"admin":{"listen":"127.0.0.1:2019"}}`, gotBody)

	got, err := c.Config(ctx)
	require.NoError(t, err)
	assert.True(t, SameConfig([]byte(`{"admin":{"listen":"127.0.0.1:2019"}}`), got))
	require.NoError(t, c.Ping(ctx))

	err = c.Load(ctx, []byte(`{"ruim":true}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
	assert.Contains(t, err.Error(), "loading config: json: ruim")
}

func TestAdmin_ErrorBodyTruncated(t *testing.T) {
	big := strings.Repeat("x", 10*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, big)
	}))
	defer srv.Close()

	c := &AdminClient{BaseURL: srv.URL + "/"}
	err := c.Load(context.Background(), []byte(`{}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
	assert.Contains(t, err.Error(), strings.Repeat("x", 2*1024))
	assert.NotContains(t, err.Error(), strings.Repeat("x", 2*1024+1))

	_, err = c.Config(context.Background())
	assert.Error(t, err)
	assert.Error(t, c.Ping(context.Background()))
}

func TestAdmin_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	c := &AdminClient{BaseURL: url}
	assert.Error(t, c.Ping(context.Background()))
}

func TestAdmin_Defaults(t *testing.T) {
	c := &AdminClient{}
	assert.Equal(t, "http://127.0.0.1:2019", c.baseURL())
	assert.Equal(t, 10*time.Second, c.httpClient().Timeout)
	assert.Equal(t, DefaultAdminURL, NewAdminClient().BaseURL)
}
