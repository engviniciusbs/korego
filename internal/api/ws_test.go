package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/events"
	"github.com/engviniciusbs/korego/internal/node"
	"github.com/engviniciusbs/korego/internal/node/nodetest"
)

func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
}

func wsEnv(t *testing.T, d api.Deps) (*env, *httptest.Server, *http.Cookie) {
	t.Helper()
	e := newEnvDeps(t, d)
	_, c, _ := e.setup(t)
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	return e, srv, c
}

func dial(t *testing.T, srv *httptest.Server, c *http.Cookie, hdr map[string]string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	h := http.Header{}
	if c != nil {
		h.Set("Cookie", c.Name+"="+c.Value)
	}
	for k, v := range hdr {
		h.Set(k, v)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	conn, resp, err := websocket.Dial(ctx, wsURL(srv), &websocket.DialOptions{HTTPHeader: h})
	if conn != nil {
		t.Cleanup(func() { conn.CloseNow() })
	}
	return conn, resp, err
}

func readMsg(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var m map[string]any
	require.NoError(t, wsjson.Read(ctx, conn, &m))
	return m
}

func sendMsg(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, wsjson.Write(ctx, conn, v))
}

func TestWS_RequiresSession(t *testing.T) {
	_, srv, _ := wsEnv(t, api.Deps{})
	_, resp, err := dial(t, srv, nil, nil)
	require.Error(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestWS_RejectsForeignOrigin(t *testing.T) {
	_, srv, c := wsEnv(t, api.Deps{})
	_, resp, err := dial(t, srv, c, map[string]string{"Origin": "https://malicioso.exemplo.com"})
	require.Error(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestWS_SubscribeReceivesEvent(t *testing.T) {
	fake := &nodetest.Fake{InfoFn: func(context.Context) (node.HostInfo, error) {
		return node.HostInfo{UptimeSeconds: 123}, nil
	}}
	hub := events.NewHub()
	hub.RegisterSource("system", api.SystemSource(fake, 20*time.Millisecond))
	_, srv, c := wsEnv(t, api.Deps{Node: fake, Hub: hub})
	conn, _, err := dial(t, srv, c, nil)
	require.NoError(t, err)

	sendMsg(t, conn, map[string]string{"type": "subscribe", "topic": "system"})
	sendMsg(t, conn, map[string]string{"type": "subscribe", "topic": "system"}) // duplicado ignorado
	m := readMsg(t, conn)
	assert.Equal(t, "event", m["type"])
	assert.Equal(t, "system", m["topic"])
	assert.Equal(t, map[string]any{"uptimeSeconds": float64(123)}, m["data"])

	sendMsg(t, conn, map[string]string{"type": "unsubscribe", "topic": "system"})
	sendMsg(t, conn, map[string]string{"type": "nada"})
	for {
		m = readMsg(t, conn) // descarta eventos em trânsito até o erro
		if m["type"] == "error" {
			break
		}
	}
	assert.Equal(t, "INVALID_MESSAGE", m["code"])
}

func TestWS_InvalidTopic(t *testing.T) {
	_, srv, c := wsEnv(t, api.Deps{})
	conn, _, err := dial(t, srv, c, nil)
	require.NoError(t, err)
	sendMsg(t, conn, map[string]string{"type": "subscribe", "topic": "../x"})
	m := readMsg(t, conn)
	assert.Equal(t, map[string]any{"type": "error", "code": "INVALID_TOPIC", "topic": "../x"}, m)
}

func TestWS_ClosesWhenSessionRevoked(t *testing.T) {
	e, srv, c := wsEnv(t, api.Deps{WSPingInterval: 50 * time.Millisecond})
	conn, _, err := dial(t, srv, c, nil)
	require.NoError(t, err)

	sess, err := e.svc.Sessions.Validate(context.Background(), c.Value)
	require.NoError(t, err)
	require.NoError(t, e.svc.DB.DeleteSession(context.Background(), sess.ID))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var m map[string]any
	err = wsjson.Read(ctx, conn, &m)
	require.Error(t, err)
	assert.Equal(t, websocket.StatusCode(4401), websocket.CloseStatus(err))
}

func TestWS_UnsubscribeStopsEventsAndGoroutines(t *testing.T) {
	hub := events.NewHub()
	hub.RegisterSource("t", func(ctx context.Context, _ string, publish func(any)) {
		tk := time.NewTicker(5 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				publish("x")
			}
		}
	})
	_, srv, c := wsEnv(t, api.Deps{Hub: hub})
	conn, _, err := dial(t, srv, c, nil)
	require.NoError(t, err)

	sendMsg(t, conn, map[string]string{"type": "subscribe", "topic": "t"})
	assert.Equal(t, "event", readMsg(t, conn)["type"])
	sendMsg(t, conn, map[string]string{"type": "unsubscribe", "topic": "t"})
	// Um erro sentinela vem depois do unsubscribe (mesmo leitor, em ordem).
	sendMsg(t, conn, map[string]string{"type": "nada"})
	for readMsg(t, conn)["type"] != "error" {
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	var m map[string]any
	assert.Error(t, wsjson.Read(ctx, conn, &m), "não deve chegar evento após unsubscribe")
}

func TestWS_SubscribeUnsubscribeDoesNotLeakGoroutines(t *testing.T) {
	hub := events.NewHub()
	_, srv, c := wsEnv(t, api.Deps{Hub: hub})
	conn, _, err := dial(t, srv, c, nil)
	require.NoError(t, err)
	base := runtime.NumGoroutine()
	for i := 0; i < 200; i++ {
		sendMsg(t, conn, map[string]string{"type": "subscribe", "topic": "t"})
		sendMsg(t, conn, map[string]string{"type": "unsubscribe", "topic": "t"})
	}
	sendMsg(t, conn, map[string]string{"type": "nada"})
	for readMsg(t, conn)["type"] != "error" {
	}
	require.Eventually(t, func() bool { return runtime.NumGoroutine() <= base+5 },
		2*time.Second, 20*time.Millisecond)
}
