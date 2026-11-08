package github

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

	"github.com/engviniciusbs/korego/internal/secrets"
	"github.com/engviniciusbs/korego/internal/store"
)

func TestConvertManifest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/app-manifests/abc_123/conversions", r.URL.Path)
		assert.Empty(t, r.Header.Get("Authorization"))
		assert.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
		assert.Equal(t, "2022-11-28", r.Header.Get("X-GitHub-Api-Version"))
		assert.Equal(t, "KoreGO", r.Header.Get("User-Agent"))
		_, _ = w.Write([]byte(`{"id":7,"slug":"korego-x","pem":"PEM","webhook_secret":"ws","html_url":"https://github.com/apps/korego-x","owner":{"login":"dono"}}`))
	}))
	defer srv.Close()
	c := &Client{APIURL: srv.URL}
	got, err := c.ConvertManifest(context.Background(), "abc_123")
	require.NoError(t, err)
	assert.Equal(t, Conversion{ID: 7, Slug: "korego-x", PEM: "PEM", WebhookSecret: "ws", HTMLURL: "https://github.com/apps/korego-x", Owner: "dono"}, got)

	_, err = c.ConvertManifest(context.Background(), "../x")
	assert.Error(t, err)
}

func TestClient_ErrorHasStatusAndMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer srv.Close()
	c := &Client{APIURL: srv.URL}
	_, err := c.Installations(context.Background(), "SEGREDO-JWT")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
	assert.Contains(t, err.Error(), "Bad credentials")
	assert.NotContains(t, err.Error(), "SEGREDO-JWT")
}

func TestRepos_Pagination(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"repositories":[{"full_name":"o/c","default_branch":"dev","private":false}]}`))
			return
		}
		assert.Equal(t, "100", r.URL.Query().Get("per_page"))
		w.Header().Set("Link", fmt.Sprintf(`<%s/installation/repositories?per_page=100&page=2>; rel="next", <%s/x>; rel="last"`, srv.URL, srv.URL))
		_, _ = w.Write([]byte(`{"repositories":[{"full_name":"o/a","default_branch":"main","private":true},{"full_name":"o/b","default_branch":"main","private":false}]}`))
	}))
	defer srv.Close()
	repos, err := (&Client{APIURL: srv.URL}).Repos(context.Background(), "tok")
	require.NoError(t, err)
	require.Len(t, repos, 3)
	assert.Equal(t, Repo{FullName: "o/a", DefaultBranch: "main", Private: true}, repos[0])
	assert.Equal(t, "o/c", repos[2].FullName)
}

func TestRepos_IgnoresForeignNextLink(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Link", `<http://evil.example.com/x>; rel="next"`)
		_, _ = w.Write([]byte(`{"repositories":[]}`))
	}))
	defer srv.Close()
	_, err := (&Client{APIURL: srv.URL}).Repos(context.Background(), "tok")
	require.NoError(t, err)
	assert.EqualValues(t, 1, atomic.LoadInt32(&hits))
}

type fakeGH struct {
	srv       *httptest.Server
	tokenPOST int32
	expires   time.Time
	fail      [3]int32 // != 0: access_tokens da instalação devolve 403
}

func newFakeGH(t *testing.T, expires time.Time, repos map[int64][]string) *fakeGH {
	f := &fakeGH{expires: expires}
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations", func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey"))
		var ins []map[string]any
		for _, id := range []int64{1, 2} {
			if _, ok := repos[id]; ok {
				ins = append(ins, map[string]any{"id": id, "account": map[string]any{"login": fmt.Sprintf("acc%d", id)}})
			}
		}
		_ = json.NewEncoder(w).Encode(ins)
	})
	for _, id := range []int64{1, 2} {
		id := id
		mux.HandleFunc(fmt.Sprintf("/app/installations/%d/access_tokens", id), func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "POST", r.Method)
			atomic.AddInt32(&f.tokenPOST, 1)
			if atomic.LoadInt32(&f.fail[id]) != 0 {
				w.WriteHeader(403)
				_, _ = w.Write([]byte(`{"message":"This installation has been suspended"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"token": fmt.Sprintf("tok%d", id), "expires_at": f.expires.UTC().Format(time.RFC3339)})
		})
	}
	mux.HandleFunc("/installation/repositories", func(w http.ResponseWriter, r *http.Request) {
		var id int64
		_, _ = fmt.Sscanf(r.Header.Get("Authorization"), "Bearer tok%d", &id)
		var rs []map[string]any
		for _, n := range repos[id] {
			rs = append(rs, map[string]any{"full_name": n, "default_branch": "main", "private": true})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"repositories": rs})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func newService(t *testing.T, f *fakeGH, now func() time.Time) *Service {
	db := store.OpenMemory(t)
	box, err := secrets.NewBox(make([]byte, 32))
	require.NoError(t, err)
	enc, err := box.Encrypt(pemPKCS1(testKey(t)))
	require.NoError(t, err)
	require.NoError(t, db.SaveGitHubApp(context.Background(), &store.GitHubApp{AppID: 9, Slug: "s", Owner: "o", HTMLURL: "u",
		PrivateKeyEnc: enc, WebhookSecretEnc: "x", CreatedAt: now()}))
	return &Service{DB: db, Box: box, Client: &Client{APIURL: f.srv.URL}, Now: now}
}

func TestCloneToken_CachesUntilExpiry(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := newFakeGH(t, now.Add(time.Hour), map[int64][]string{1: {"o/a"}})
	s := newService(t, f, func() time.Time { return now })

	for i := 0; i < 2; i++ {
		tok, err := s.CloneToken(context.Background(), "o/a")
		require.NoError(t, err)
		assert.Equal(t, "tok1", tok)
	}
	assert.EqualValues(t, 1, atomic.LoadInt32(&f.tokenPOST))

	// Faltando 4 min para expirar: renova.
	now = now.Add(time.Hour - 4*time.Minute)
	_, err := s.CloneToken(context.Background(), "o/a")
	require.NoError(t, err)
	assert.EqualValues(t, 2, atomic.LoadInt32(&f.tokenPOST))
}

func TestCloneToken_FindsSecondInstallation(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := newFakeGH(t, now.Add(time.Hour), map[int64][]string{1: {"o/a"}, 2: {"o/b"}})
	s := newService(t, f, func() time.Time { return now })
	tok, err := s.CloneToken(context.Background(), "o/b")
	require.NoError(t, err)
	assert.Equal(t, "tok2", tok)
}

func TestCloneToken_RepoNotInstalled(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := newFakeGH(t, now.Add(time.Hour), map[int64][]string{1: {"o/a"}})
	s := newService(t, f, func() time.Time { return now })
	_, err := s.CloneToken(context.Background(), "o/zzz")
	assert.ErrorIs(t, err, ErrRepoNotInstalled)
}

func TestService_NotConnected(t *testing.T) {
	db := store.OpenMemory(t)
	box, _ := secrets.NewBox(make([]byte, 32))
	s := &Service{DB: db, Box: box, Client: &Client{APIURL: "http://127.0.0.1:1"}}
	_, ok, err := s.Connected(context.Background())
	require.NoError(t, err)
	assert.False(t, ok)
	_, err = s.CloneToken(context.Background(), "o/a")
	assert.ErrorIs(t, err, ErrNotConnected)
	_, err = s.Repos(context.Background())
	assert.ErrorIs(t, err, ErrNotConnected)
}

func TestService_ReposDedupe(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := newFakeGH(t, now.Add(time.Hour), map[int64][]string{1: {"o/a", "o/b"}, 2: {"o/b", "o/c"}})
	s := newService(t, f, func() time.Time { return now })
	got, err := s.Repos(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "o/a", got[0].FullName)
	assert.EqualValues(t, 1, got[0].InstallationID)
	assert.Equal(t, "o/c", got[2].FullName)
	assert.EqualValues(t, 2, got[2].InstallationID)
}

func TestCloneToken_SkipsBrokenInstallation(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := newFakeGH(t, now.Add(time.Hour), map[int64][]string{1: {"o/a"}, 2: {"o/b"}})
	f.fail[1] = 1
	s := newService(t, f, func() time.Time { return now })
	tok, err := s.CloneToken(context.Background(), "o/b")
	require.NoError(t, err)
	assert.Equal(t, "tok2", tok)

	// Nenhuma instalação enxerga o repo: devolve o último erro, não ErrRepoNotInstalled.
	_, err = s.CloneToken(context.Background(), "o/zzz")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrRepoNotInstalled)
	assert.Contains(t, err.Error(), "403")
}

func TestService_ReposSkipsBrokenInstallation(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := newFakeGH(t, now.Add(time.Hour), map[int64][]string{1: {"o/a"}, 2: {"o/b", "o/c"}})
	f.fail[1] = 1
	s := newService(t, f, func() time.Time { return now })
	got, err := s.Repos(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "o/b", got[0].FullName)

	// Todas falhando (serviço novo, sem tokens em cache): erro.
	f.fail[2] = 1
	s2 := newService(t, f, func() time.Time { return now })
	_, err = s2.Repos(context.Background())
	assert.Error(t, err)
}

func TestCloneToken_EvictsCacheOnTokenError(t *testing.T) {
	now := time.Unix(1700000000, 0)
	// Tokens de 6 min: com a folga de 5 min, renovam após 1 min (bem dentro do TTL de 10 min do cache de repo).
	f := newFakeGH(t, now.Add(6*time.Minute), map[int64][]string{1: {"o/a"}, 2: {"o/a"}})
	s := newService(t, f, func() time.Time { return now })
	tok, err := s.CloneToken(context.Background(), "o/a")
	require.NoError(t, err)
	assert.Equal(t, "tok1", tok)

	// Dois minutos depois: o cache repo->instalação 1 ainda vale, mas o token
	// precisa ser renovado e a renovação falha. Deve despejar e varrer de novo.
	now = now.Add(2 * time.Minute)
	f.expires = now.Add(6 * time.Minute)
	f.fail[1] = 1
	require.True(t, now.Before(s.repos["o/a"].until), "cache de repo deve continuar válido")
	tok, err = s.CloneToken(context.Background(), "o/a")
	require.NoError(t, err)
	assert.Equal(t, "tok2", tok)
}

func TestCloneToken_RepoNameCaseInsensitive(t *testing.T) {
	now := time.Unix(1700000000, 0)
	f := newFakeGH(t, now.Add(time.Hour), map[int64][]string{1: {"Dono/App"}})
	s := newService(t, f, func() time.Time { return now })
	tok, err := s.CloneToken(context.Background(), "dono/app")
	require.NoError(t, err)
	assert.Equal(t, "tok1", tok)
}

func TestConvertManifest_ErrorRedactsCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()
	_, err := (&Client{APIURL: srv.URL}).ConvertManifest(context.Background(), "codigo-secreto")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "codigo-secreto")
	assert.Contains(t, err.Error(), "404")

	srv.Close()
	_, err = (&Client{APIURL: srv.URL}).ConvertManifest(context.Background(), "codigo-secreto")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "codigo-secreto")
}
