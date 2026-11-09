package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/store"
)

func v4List(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "198.51.%d.0/24\n", i)
	}
	return b.String()
}

const v6Good = "2001:db8::/32\n2001:db8:1::/48\n"

// server serve v4 em /ips-v4 e v6 em /ips-v6 com os corpos e o status dados.
func server(t *testing.T, v4, v6 string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		switch r.URL.Path {
		case "/ips-v4":
			_, _ = w.Write([]byte(v4))
		case "/ips-v6":
			_, _ = w.Write([]byte(v6))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newRanges(t *testing.T, db *store.DB, srv *httptest.Server) *Ranges {
	t.Helper()
	r := New(db, srv.Client())
	r.V4URL = srv.URL + "/ips-v4"
	r.V6URL = srv.URL + "/ips-v6"
	return r
}

func TestRanges_Embedded(t *testing.T) {
	r := New(nil, nil)
	got := r.Get()
	assert.Len(t, got, 22)
	assert.Contains(t, got, "173.245.48.0/20")
	assert.Contains(t, got, "2c0f:f248::/32")
	// Get devolve cópia.
	got[0] = "x"
	assert.NotEqual(t, "x", r.Get()[0])
}

func TestRanges_Refresh(t *testing.T) {
	cases := []struct {
		name     string
		v4, v6   string
		status   int
		accepted bool
	}{
		{"lista boa", v4List(12), v6Good, 200, true},
		{"html ignorado", "<html><body>erro</body></html>", "<html></html>", 200, false},
		{"5 CIDRs v4 ignorados", v4List(5), v6Good, 200, false},
		{"sem v6 ignorado", v4List(12), "", 200, false},
		{"500 ignorado", "", "", 500, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := store.OpenMemory(t)
			srv := server(t, tc.v4, tc.v6, tc.status)
			r := newRanges(t, db, srv)
			err := r.Refresh(context.Background())
			if tc.accepted {
				require.NoError(t, err)
				got := r.Get()
				assert.Len(t, got, 14)
				assert.Contains(t, got, "198.51.1.0/24")
				assert.Contains(t, got, "2001:db8::/32")
				// Persistida: um novo Ranges sobre o mesmo banco usa a lista.
				assert.Equal(t, got, New(db, nil).Get())
				return
			}
			require.Error(t, err)
			assert.Len(t, r.Get(), 22, "mantém a lista embutida")
			_, ok, _ := db.GetSetting(context.Background(), settingKey)
			assert.False(t, ok, "nada persistido")
		})
	}
}

func TestRanges_StoredListUsedOnStartup(t *testing.T) {
	db := store.OpenMemory(t)
	v4 := strings.Fields(v4List(10))
	raw, err := json.Marshal(list{V4: v4, V6: []string{"2001:db8::/32"}})
	require.NoError(t, err)
	require.NoError(t, db.SetSetting(context.Background(), settingKey, string(raw)))
	got := New(db, nil).Get()
	assert.Equal(t, append(v4, "2001:db8::/32"), got)
}

func TestRanges_InvalidStoredListFallsBackToEmbedded(t *testing.T) {
	db := store.OpenMemory(t)
	require.NoError(t, db.SetSetting(context.Background(), settingKey, `{"v4":["198.51.100.0/24"],"v6":[]}`))
	assert.Len(t, New(db, nil).Get(), 22)
}

func TestRanges_RunRefreshesAtStartAndStops(t *testing.T) {
	srv := server(t, v4List(12), v6Good, 200)
	r := newRanges(t, nil, srv)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	require.Eventually(t, func() bool { return len(r.Get()) == 14 }, 2*time.Second, 5*time.Millisecond)
	cancel()
	<-done
}

func TestRanges_RejectsTooBroadCIDRs(t *testing.T) {
	// 0.0.0.0/0 e ::/0 não contam nem entram.
	broad := "0.0.0.0/0\n" + v4List(5)
	srv := server(t, broad, "::/0\n", 200)
	r := newRanges(t, nil, srv)
	require.Error(t, r.Refresh(context.Background()))
	assert.Len(t, r.Get(), 22)

	srv = server(t, "0.0.0.0/0\n1.0.0.0/7\n"+v4List(10), "::/0\n2000::/16\n"+v6Good, 200)
	r = newRanges(t, nil, srv)
	require.NoError(t, r.Refresh(context.Background()))
	got := r.Get()
	assert.NotContains(t, got, "0.0.0.0/0")
	assert.NotContains(t, got, "1.0.0.0/7")
	assert.NotContains(t, got, "::/0")
	assert.NotContains(t, got, "2000::/16")
	assert.Len(t, got, 12)
}

func TestRanges_RunRetriesSoonAfterFailedRefresh(t *testing.T) {
	var calls atomic.Int32
	good := server(t, v4List(12), v6Good, 200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 1 { // primeira chamada (v4 do boot) falha
			w.WriteHeader(500)
			return
		}
		http.Redirect(w, r, good.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	r := newRanges(t, nil, srv)
	r.retryInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	require.Eventually(t, func() bool { return len(r.Get()) == 14 }, 2*time.Second, 5*time.Millisecond)
	cancel()
	<-done
}
