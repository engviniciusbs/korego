// Package events implementa o hub de eventos em memória usado pelo WebSocket.
package events

import (
	"context"
	"strings"
	"sync"
)

const subscriberBuffer = 64

// Event é uma mensagem publicada em um tópico.
type Event struct {
	Topic string
	Data  any
}

// SourceFunc produz eventos de um tópico enquanto ctx não for cancelado.
type SourceFunc func(ctx context.Context, topic string, publish func(data any))

type subscriber struct{ ch chan Event }

type topicState struct {
	subs   map[*subscriber]struct{}
	cancel context.CancelFunc // nil quando não há fonte
	done   chan struct{}
}

type source struct {
	prefix string
	fn     SourceFunc
}

// Hub distribui eventos aos assinantes. Seguro para uso concorrente.
type Hub struct {
	mu      sync.Mutex
	topics  map[string]*topicState
	sources []source
}

// NewHub cria um hub vazio.
func NewHub() *Hub { return &Hub{topics: map[string]*topicState{}} }

// RegisterSource registra uma fonte para tópicos iguais a prefix ou iniciados
// por prefix+":".
func (h *Hub) RegisterSource(prefix string, fn SourceFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sources = append(h.sources, source{prefix: prefix, fn: fn})
}

func (h *Hub) sourceFor(topic string) SourceFunc {
	for _, s := range h.sources {
		if topic == s.prefix || strings.HasPrefix(topic, s.prefix+":") {
			return s.fn
		}
	}
	return nil
}

// Publish entrega data aos assinantes do tópico sem nunca bloquear; se o
// buffer de um assinante estiver cheio, o evento é descartado para ele.
func (h *Hub) Publish(topic string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ts := h.topics[topic]
	if ts == nil {
		return
	}
	ev := Event{Topic: topic, Data: data}
	for s := range ts.subs {
		select {
		case s.ch <- ev:
		default:
		}
	}
}

// Subscribe assina um tópico. cancel é idempotente.
func (h *Hub) Subscribe(topic string) (<-chan Event, func()) {
	sub := &subscriber{ch: make(chan Event, subscriberBuffer)}
	h.mu.Lock()
	ts := h.topics[topic]
	if ts == nil {
		ts = &topicState{subs: map[*subscriber]struct{}{}}
		h.topics[topic] = ts
		if fn := h.sourceFor(topic); fn != nil {
			ctx, cancel := context.WithCancel(context.Background())
			ts.cancel = cancel
			ts.done = make(chan struct{})
			go func() {
				defer close(ts.done)
				fn(ctx, topic, func(data any) {
					if ctx.Err() == nil {
						h.Publish(topic, data)
					}
				})
			}()
		}
	}
	ts.subs[sub] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			delete(ts.subs, sub)
			close(sub.ch) // seguro: Publish envia sob o mesmo lock
			if len(ts.subs) == 0 {
				if h.topics[topic] == ts {
					delete(h.topics, topic)
				}
				if ts.cancel != nil {
					ts.cancel()
				}
			}
		})
	}
	return sub.ch, cancel
}
