package events_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/events"
)

func recv(t *testing.T, ch <-chan events.Event) events.Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timeout aguardando evento")
		return events.Event{}
	}
}

func TestHub_PublishSubscribe(t *testing.T) {
	h := events.NewHub()
	h.Publish("a", 1) // sem assinantes: no-op
	ch, cancel := h.Subscribe("a")
	other, cancelOther := h.Subscribe("b")
	defer cancelOther()
	h.Publish("a", 42)
	ev := recv(t, ch)
	assert.Equal(t, events.Event{Topic: "a", Data: 42}, ev)
	assert.Empty(t, other)
	cancel()
	cancel() // idempotente
	h.Publish("a", 43)
	assert.Empty(t, ch)
}

func TestHub_SlowSubscriberDoesNotBlock(t *testing.T) {
	h := events.NewHub()
	_, cancelA := h.Subscribe("t") // nunca lê
	defer cancelA()
	b, cancelB := h.Subscribe("t")
	defer cancelB()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 1; i <= 1000; i++ {
			h.Publish("t", i)
			if i%32 == 0 { // B drena para nunca encher
				for len(b) > 0 {
					<-b
				}
			}
		}
		h.Publish("t", "ultimo")
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Publish bloqueou com assinante lento")
	}
	var last events.Event
	for len(b) > 0 {
		last = <-b
	}
	assert.Equal(t, "ultimo", last.Data)
}

func TestHub_SourceStartsAndStops(t *testing.T) {
	h := events.NewHub()
	var starts atomic.Int32
	started := make(chan string, 4)
	stopped := make(chan struct{})
	h.RegisterSource("sys", func(ctx context.Context, topic string, publish func(any)) {
		starts.Add(1)
		started <- topic
		publish("oi")
		<-ctx.Done()
		close(stopped)
	})

	ch1, c1 := h.Subscribe("sys:x")
	require.Equal(t, "sys:x", <-started)
	assert.Equal(t, "oi", recv(t, ch1).Data)
	_, c2 := h.Subscribe("sys:x")
	_, cOther := h.Subscribe("outro") // sem fonte
	defer cOther()
	require.Eventually(t, func() bool { return starts.Load() == 1 }, time.Second, 5*time.Millisecond)
	assert.Never(t, func() bool { return starts.Load() > 1 }, 50*time.Millisecond, 5*time.Millisecond)

	c1()
	select {
	case <-stopped:
		t.Fatal("fonte parou com assinante restante")
	case <-time.After(30 * time.Millisecond):
	}
	c2()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("fonte não foi cancelada")
	}
}
