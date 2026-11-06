package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/docker/dockertest"
	"github.com/engviniciusbs/korego/internal/node/metrics"
	"github.com/engviniciusbs/korego/internal/node/nodetest"
	"github.com/engviniciusbs/korego/internal/store"
)

const cid = "0123456789ab"

func containersEnv(t *testing.T, f *dockertest.Fake) (*env, *http.Cookie, string) {
	t.Helper()
	e := newEnvDeps(t, api.Deps{
		Node:    &nodetest.Fake{DockerClient: f},
		Metrics: fakeLatest{s: metrics.Sample{CPUPercent: 1}, ok: true},
		DB:      store.OpenMemory(t),
	})
	_, c, csrf := e.setup(t)
	return e, c, csrf
}

func TestContainers_List(t *testing.T) {
	f := &dockertest.Fake{ListFn: func(context.Context) ([]docker.Container, error) {
		return []docker.Container{
			{ID: "bbbbbbbbbbbb", Name: "web", Project: "zeta"},
			{ID: "cccccccccccc", Name: "db", Project: "alfa"},
			{ID: "aaaaaaaaaaaa", Name: "api", Project: "alfa"},
		}, nil
	}}
	e, c, _ := containersEnv(t, f)
	w := e.do(t, http.MethodGet, "/api/containers", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	list := decode(t, w)["containers"].([]any)
	require.Len(t, list, 3)
	var names []string
	for _, it := range list {
		names = append(names, it.(map[string]any)["name"].(string))
	}
	assert.Equal(t, []string{"api", "db", "web"}, names)
}

func TestContainers_DockerDown503(t *testing.T) {
	f := &dockertest.Fake{ListFn: func(context.Context) ([]docker.Container, error) { return nil, docker.ErrUnavailable }}
	e, c, _ := containersEnv(t, f)
	w := e.do(t, http.MethodGet, "/api/containers", nil, withCookie(c))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "DOCKER_UNAVAILABLE", decode(t, w)["error"].(map[string]any)["code"])

	w = e.do(t, http.MethodGet, "/api/metrics/current", nil, withCookie(c))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestContainers_DeadlineIs503(t *testing.T) {
	f := &dockertest.Fake{ListFn: func(context.Context) ([]docker.Container, error) { return nil, context.DeadlineExceeded }}
	e, c, _ := containersEnv(t, f)
	w := e.do(t, http.MethodGet, "/api/containers", nil, withCookie(c))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "DOCKER_UNAVAILABLE", decode(t, w)["error"].(map[string]any)["code"])
}

func TestContainers_InvalidID400(t *testing.T) {
	f := &dockertest.Fake{}
	e, c, csrf := containersEnv(t, f)
	for _, id := range []string{"..%2Fetc", "XYZ", "0123456789a", strings.Repeat("a", 65)} {
		for _, p := range []string{"", "/logs", "/logs/download"} {
			w := e.do(t, http.MethodGet, "/api/containers/"+id+p, nil, withCookie(c))
			require.Equal(t, http.StatusBadRequest, w.Code, id+p+" "+w.Body.String())
			assert.Equal(t, "INVALID_CONTAINER_ID", decode(t, w)["error"].(map[string]any)["code"])
		}
		w := e.do(t, http.MethodPost, "/api/containers/"+id+"/stop", nil, withCookie(c), withHeader("X-CSRF-Token", csrf))
		require.Equal(t, http.StatusBadRequest, w.Code, id)
		w = e.do(t, http.MethodDelete, "/api/containers/"+id, nil, withCookie(c), withHeader("X-CSRF-Token", csrf))
		require.Equal(t, http.StatusBadRequest, w.Code, id)
	}
}

func TestContainers_NotFound404(t *testing.T) {
	f := &dockertest.Fake{InspectFn: func(context.Context, string) (docker.Details, error) {
		return docker.Details{}, docker.ErrNotFound
	}}
	e, c, csrf := containersEnv(t, f)
	w := e.do(t, http.MethodGet, "/api/containers/"+cid, nil, withCookie(c))
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "CONTAINER_NOT_FOUND", decode(t, w)["error"].(map[string]any)["code"])
	w = e.do(t, http.MethodPost, "/api/containers/"+cid+"/start", nil, withCookie(c), withHeader("X-CSRF-Token", csrf))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestContainers_ActionsRequireCSRF(t *testing.T) {
	called := false
	f := &dockertest.Fake{StopFn: func(context.Context, string) error { called = true; return nil }}
	e, c, _ := containersEnv(t, f)
	w := e.do(t, http.MethodPost, "/api/containers/"+cid+"/stop", nil, withCookie(c))
	assert.Equal(t, http.StatusForbidden, w.Code)
	w = e.do(t, http.MethodDelete, "/api/containers/"+cid, nil, withCookie(c))
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.False(t, called)
	w = e.do(t, http.MethodGet, "/api/containers", nil)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestContainers_RemoveRunning409(t *testing.T) {
	for _, st := range []string{"running", "restarting", "paused"} {
		removed := false
		f := &dockertest.Fake{
			InspectFn: func(context.Context, string) (docker.Details, error) {
				return docker.Details{Container: docker.Container{ID: cid, Name: "web", State: st}}, nil
			},
			RemoveFn: func(context.Context, string) error { removed = true; return nil },
		}
		e, c, csrf := containersEnv(t, f)
		w := e.do(t, http.MethodDelete, "/api/containers/"+cid, nil, withCookie(c), withHeader("X-CSRF-Token", csrf))
		require.Equal(t, http.StatusConflict, w.Code, st)
		assert.Equal(t, "CONTAINER_RUNNING", decode(t, w)["error"].(map[string]any)["code"])
		assert.False(t, removed)

		w = e.do(t, http.MethodGet, "/api/audit", nil, withCookie(c))
		it := decode(t, w)["items"].([]any)[0].(map[string]any)
		assert.Equal(t, "container.remove", it["action"])
		assert.Equal(t, false, it["metadata"].(map[string]any)["ok"])
	}
}

func TestContainers_RemoveStopped(t *testing.T) {
	removed := false
	f := &dockertest.Fake{
		InspectFn: func(context.Context, string) (docker.Details, error) {
			return docker.Details{Container: docker.Container{ID: cid, Name: "web", State: "exited"}}, nil
		},
		RemoveFn: func(context.Context, string) error { removed = true; return nil },
	}
	e, c, csrf := containersEnv(t, f)
	w := e.do(t, http.MethodDelete, "/api/containers/"+cid, nil, withCookie(c), withHeader("X-CSRF-Token", csrf))
	require.Equal(t, http.StatusNoContent, w.Code)
	assert.True(t, removed)
}

func TestContainers_RemoveDockerConflict409(t *testing.T) {
	f := &dockertest.Fake{
		InspectFn: func(context.Context, string) (docker.Details, error) {
			return docker.Details{Container: docker.Container{ID: cid, Name: "web", State: "exited"}}, nil
		},
		RemoveFn: func(context.Context, string) error { return docker.ErrConflict },
	}
	e, c, csrf := containersEnv(t, f)
	w := e.do(t, http.MethodDelete, "/api/containers/"+cid, nil, withCookie(c), withHeader("X-CSRF-Token", csrf))
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "CONTAINER_RUNNING", decode(t, w)["error"].(map[string]any)["code"])
}

func TestContainers_ActionAudited(t *testing.T) {
	var failStop bool
	f := &dockertest.Fake{
		InspectFn: func(context.Context, string) (docker.Details, error) {
			return docker.Details{Container: docker.Container{ID: cid, Name: "web", Managed: true}}, nil
		},
		StopFn: func(context.Context, string) error {
			if failStop {
				return docker.ErrUnavailable
			}
			return nil
		},
		StartFn:   func(context.Context, string) error { return nil },
		RestartFn: func(context.Context, string) error { return nil },
	}
	e, c, csrf := containersEnv(t, f)
	for _, a := range []string{"start", "restart", "stop"} {
		w := e.do(t, http.MethodPost, "/api/containers/"+cid+"/"+a, nil, withCookie(c), withHeader("X-CSRF-Token", csrf))
		require.Equal(t, http.StatusNoContent, w.Code, a)
	}
	failStop = true
	w := e.do(t, http.MethodPost, "/api/containers/"+cid+"/stop", nil, withCookie(c), withHeader("X-CSRF-Token", csrf))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)

	w = e.do(t, http.MethodGet, "/api/audit", nil, withCookie(c))
	items := decode(t, w)["items"].([]any)
	fail := items[0].(map[string]any)
	assert.Equal(t, "container.stop", fail["action"])
	assert.Equal(t, "web", fail["target"])
	assert.Equal(t, false, fail["metadata"].(map[string]any)["ok"])
	ok := items[1].(map[string]any)
	assert.Equal(t, "container.stop", ok["action"])
	assert.Equal(t, "web", ok["target"])
	m := ok["metadata"].(map[string]any)
	assert.Equal(t, true, m["ok"])
	assert.Equal(t, true, m["managed"])
	assert.Equal(t, cid, m["id"])
	assert.Equal(t, "container.restart", items[2].(map[string]any)["action"])
}

func TestContainers_ActionAuditedAfterClientGone(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	stopCtxErr := make(chan error, 1)
	f := &dockertest.Fake{
		InspectFn: func(context.Context, string) (docker.Details, error) {
			return docker.Details{Container: docker.Container{ID: cid, Name: "web"}}, nil
		},
		StopFn: func(ctx context.Context, _ string) error {
			close(started)
			<-release
			stopCtxErr <- ctx.Err()
			return nil
		},
	}
	e, c, csrf := containersEnv(t, f)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.do(t, http.MethodPost, "/api/containers/"+cid+"/stop", nil, withCookie(c), withHeader("X-CSRF-Token", csrf),
			func(r *http.Request) { *r = *r.WithContext(ctx) })
	}()
	<-started
	cancel() // o cliente desconecta no meio do stop
	close(release)
	<-done

	// A mutação não é cancelada junto com a requisição e o registro existe.
	require.NoError(t, <-stopCtxErr)
	w := e.do(t, http.MethodGet, "/api/audit", nil, withCookie(c))
	items := decode(t, w)["items"].([]any)
	require.NotEmpty(t, items)
	last := items[0].(map[string]any)
	assert.Equal(t, "container.stop", last["action"])
	assert.Equal(t, true, last["metadata"].(map[string]any)["ok"])
}

func TestContainers_DetailsHasNoEnvValues(t *testing.T) {
	f := &dockertest.Fake{InspectFn: func(context.Context, string) (docker.Details, error) {
		return docker.Details{Container: docker.Container{ID: cid, Name: "web"}, EnvNames: []string{"DB_PASSWORD"}}, nil
	}}
	e, c, _ := containersEnv(t, f)
	w := e.do(t, http.MethodGet, "/api/containers/"+cid, nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "DB_PASSWORD")
	assert.NotContains(t, w.Body.String(), "s3cr3t")
	assert.NotContains(t, w.Body.String(), "DB_PASSWORD=")
}

func TestContainers_LogsTailBounds(t *testing.T) {
	var got docker.LogOptions
	f := &dockertest.Fake{LogsFn: func(_ context.Context, _ string, o docker.LogOptions, fn func(docker.LogLine)) error {
		got = o
		fn(docker.LogLine{TS: "0000000000000000001", Stream: "stdout", Text: "oi"})
		return nil
	}}
	e, c, _ := containersEnv(t, f)
	for _, q := range []string{"tail=0", "tail=9999", "tail=abc", "tail=-1"} {
		w := e.do(t, http.MethodGet, "/api/containers/"+cid+"/logs?"+q, nil, withCookie(c))
		require.Equal(t, http.StatusBadRequest, w.Code, q)
		assert.Equal(t, "INVALID_TAIL", decode(t, w)["error"].(map[string]any)["code"])
	}
	w := e.do(t, http.MethodGet, "/api/containers/"+cid+"/logs", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, 500, got.Tail)
	assert.False(t, got.Follow)
	assert.Len(t, decode(t, w)["lines"], 1)
	w = e.do(t, http.MethodGet, "/api/containers/"+cid+"/logs?tail=5000", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 5000, got.Tail)
}

func TestContainers_Download(t *testing.T) {
	var got docker.LogOptions
	f := &dockertest.Fake{
		InspectFn: func(context.Context, string) (docker.Details, error) {
			return docker.Details{Container: docker.Container{ID: cid, Name: "meu.app-1"}}, nil
		},
		LogsFn: func(_ context.Context, _ string, o docker.LogOptions, fn func(docker.LogLine)) error {
			got = o
			fn(docker.LogLine{TS: "1700000000123456789", Stream: "stdout", Text: "primeira"})
			fn(docker.LogLine{TS: "1700000001000000000", Stream: "stderr", Text: "segunda"})
			return nil
		},
	}
	e, c, _ := containersEnv(t, f)
	w := e.do(t, http.MethodGet, "/api/containers/"+cid+"/logs/download", nil, withCookie(c))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Equal(t, `attachment; filename="meu.app-1-logs.txt"`, w.Header().Get("Content-Disposition"))
	assert.Equal(t, 0, got.Tail)
	assert.Equal(t, "2023-11-14T22:13:20.123456789Z stdout primeira\n2023-11-14T22:13:21Z stderr segunda\n", w.Body.String())
}

func TestContainers_DownloadErrorBeforeLines(t *testing.T) {
	f := &dockertest.Fake{
		InspectFn: func(context.Context, string) (docker.Details, error) {
			return docker.Details{Container: docker.Container{ID: cid, Name: "x"}}, nil
		},
		LogsFn: func(context.Context, string, docker.LogOptions, func(docker.LogLine)) error {
			return docker.ErrUnavailable
		},
	}
	e, c, _ := containersEnv(t, f)
	w := e.do(t, http.MethodGet, "/api/containers/"+cid+"/logs/download", nil, withCookie(c))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Empty(t, w.Header().Get("Content-Disposition"))
}
