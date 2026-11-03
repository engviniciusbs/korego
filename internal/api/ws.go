package api

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"time"

	"net/http"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/engviniciusbs/korego/internal/auth"
	"github.com/engviniciusbs/korego/internal/events"
)

const (
	wsReadLimit      = 4 << 10
	wsOutBuffer      = 64
	wsDefaultPing    = 30 * time.Second
	wsPingTimeout    = 10 * time.Second
	wsStatusExpired  = websocket.StatusCode(4401)
	wsReasonExpired  = "session expired"
	wsWriteTimeout   = 10 * time.Second
	maxTopicsPerConn = 64
)

var topicRe = regexp.MustCompile(`^[a-z][a-z-]*(:[A-Za-z0-9_.-]{1,128})?$`)

type wsHandlers struct {
	svc          *auth.Service
	hub          *events.Hub
	pingInterval time.Duration
}

type wsClientMsg struct {
	Type  string `json:"type"`
	Topic string `json:"topic"`
}

type wsEventMsg struct {
	Type  string `json:"type"`
	Topic string `json:"topic"`
	Data  any    `json:"data"`
}

type wsErrorMsg struct {
	Type  string `json:"type"`
	Code  string `json:"code"`
	Topic string `json:"topic,omitempty"`
}

func (h wsHandlers) serve(w http.ResponseWriter, r *http.Request) {
	// OriginPatterns vazio: a biblioteca só aceita Origin igual ao Host.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept já respondeu (ex.: 403 para origem estrangeira).
	}
	defer c.CloseNow()
	c.SetReadLimit(wsReadLimit)

	var token string
	if ck, err := r.Cookie(sessionCookieName); err == nil {
		token = ck.Value
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	out := make(chan any, wsOutBuffer)
	send := func(v any) {
		select {
		case out <- v:
		default: // buffer cheio: descarta
		}
	}

	var (
		mu   sync.Mutex
		subs = map[string]func(){}
		wg   sync.WaitGroup
	)
	defer func() {
		cancel()
		mu.Lock()
		for _, unsub := range subs {
			unsub()
		}
		mu.Unlock()
		wg.Wait()
	}()

	subscribe := func(topic string) {
		mu.Lock()
		defer mu.Unlock()
		if _, dup := subs[topic]; dup || len(subs) >= maxTopicsPerConn {
			return
		}
		ch, unsub := h.hub.Subscribe(topic)
		subs[topic] = unsub
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case ev, ok := <-ch:
					if !ok {
						return
					}
					send(wsEventMsg{Type: "event", Topic: ev.Topic, Data: ev.Data})
				}
			}
		}()
	}
	unsubscribe := func(topic string) {
		mu.Lock()
		defer mu.Unlock()
		if unsub, ok := subs[topic]; ok {
			unsub()
			delete(subs, topic)
		}
	}

	// Leitor: único responsável por subscribe/unsubscribe.
	go func() {
		defer cancel()
		for {
			var m wsClientMsg
			if err := wsjson.Read(ctx, c, &m); err != nil {
				return
			}
			switch m.Type {
			case "subscribe", "unsubscribe":
				if !topicRe.MatchString(m.Topic) {
					send(wsErrorMsg{Type: "error", Code: "INVALID_TOPIC", Topic: m.Topic})
					continue
				}
				if m.Type == "subscribe" {
					subscribe(m.Topic)
				} else {
					unsubscribe(m.Topic)
				}
			default:
				send(wsErrorMsg{Type: "error", Code: "INVALID_MESSAGE"})
			}
		}
	}()

	interval := h.pingInterval
	if interval <= 0 {
		interval = wsDefaultPing
	}
	ping := time.NewTicker(interval)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			c.Close(websocket.StatusNormalClosure, "")
			return
		case v := <-out:
			wctx, wcancel := context.WithTimeout(ctx, wsWriteTimeout)
			err := wsjson.Write(wctx, c, v)
			wcancel()
			if err != nil {
				return
			}
		case <-ping.C:
			if _, err := h.svc.Sessions.Validate(ctx, token); errors.Is(err, auth.ErrNoSession) {
				c.Close(wsStatusExpired, wsReasonExpired)
				return
			}
			pctx, pcancel := context.WithTimeout(ctx, wsPingTimeout)
			err := c.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		}
	}
}
