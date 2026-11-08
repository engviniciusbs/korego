package domains

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engviniciusbs/korego/internal/node/proxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runAsync(s *Service) (cancel func()) {
	ctx, c := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx)
	}()
	return func() {
		c()
		<-done
	}
}

func TestRun_ReappliesWhenOutOfSync(t *testing.T) {
	ctx := context.Background()
	s, fake := newTestService(t)
	_, _, err := s.Create(ctx, addrInput("exemplo.com"))
	require.NoError(t, err)
	before := fake.Applies()

	var calls atomic.Int32
	fake.InSyncFn = func(context.Context, []proxy.Route) (bool, error) {
		return calls.Add(1) != 2, nil // fora de sincronia só na 2ª volta
	}
	s.Interval = 5 * time.Millisecond
	stop := runAsync(s)
	require.Eventually(t, func() bool { return calls.Load() >= 4 }, 2*time.Second, time.Millisecond)
	stop()

	assert.Equal(t, before+1, fake.Applies())
	want, err := s.Routes(ctx)
	require.NoError(t, err)
	assert.Equal(t, want, fake.LastApplied())
}

func TestRun_SkipsSyncWhenNotActive(t *testing.T) {
	s, fake := newTestService(t)
	var insync atomic.Int32
	fake.EnsureFn = func(context.Context) proxy.Status { return proxy.Status{State: proxy.StateBlocked} }
	fake.InSyncFn = func(context.Context, []proxy.Route) (bool, error) { insync.Add(1); return false, nil }
	s.Interval = time.Hour // só a volta imediata
	stop := runAsync(s)
	require.Eventually(t, func() bool { return s.Status().State == proxy.StateBlocked }, 2*time.Second, time.Millisecond)
	stop()
	assert.Zero(t, insync.Load())
	assert.Zero(t, fake.Applies())
}

func TestRun_PublishesStatusChanges(t *testing.T) {
	s, fake := newTestService(t)
	seq := []proxy.Status{
		{State: proxy.StateBlocked, Process: "nginx (pid 1)", Message: "porta ocupada"},
		{State: proxy.StateBlocked, Process: "nginx (pid 1)", Message: "porta ocupada"},
		{State: proxy.StateActive},
	}
	var n atomic.Int32
	fake.EnsureFn = func(context.Context) proxy.Status {
		i := int(n.Add(1)) - 1
		if i >= len(seq) {
			i = len(seq) - 1
		}
		return seq[i]
	}
	ch, unsub := s.Hub.Subscribe("proxy")
	defer unsub()

	s.Interval = 5 * time.Millisecond
	stop := runAsync(s)
	require.Eventually(t, func() bool { return n.Load() >= 6 }, 2*time.Second, time.Millisecond)
	stop()

	var got []proxy.Status
	for len(ch) > 0 {
		ev := <-ch
		got = append(got, ev.Data.(proxy.Status))
	}
	assert.Equal(t, []proxy.Status{seq[0], seq[2]}, got)
	assert.Equal(t, proxy.StateActive, s.Status().State)
}

func TestRun_ExitsOnCancel(t *testing.T) {
	s, _ := newTestService(t)
	s.Interval = time.Hour
	stop := runAsync(s)
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run não terminou após cancelar o contexto")
	}
}

// Proxy bloqueado (nginx nas portas 80/443): o deploy não pode falhar por
// isso; a rota entra quando a reconciliação vir o proxy ativo.
func TestReapplyApp_BlockedIsNil(t *testing.T) {
	ctx := context.Background()
	s, fake := newTestService(t)
	fake.EnsureFn = func(context.Context) proxy.Status {
		return proxy.Status{State: proxy.StateBlocked, Message: "porta 80 em uso por nginx"}
	}
	fake.ApplyFn = func(context.Context, []proxy.Route) error { return proxy.ErrInactive }
	s.Reconcile(ctx)
	assert.NoError(t, s.ReapplyApp(ctx, 1))
	assert.NoError(t, s.ReapplyApp(ctx, 1))
}

func TestReapplyApp_OtherInactiveStatesFail(t *testing.T) {
	ctx := context.Background()
	for _, state := range []proxy.State{proxy.StateStarting, proxy.StateDockerUnavailable, proxy.StateError} {
		s, fake := newTestService(t)
		fake.EnsureFn = func(context.Context) proxy.Status { return proxy.Status{State: state} }
		fake.ApplyFn = func(context.Context, []proxy.Route) error { return proxy.ErrInactive }
		s.Reconcile(ctx)
		assert.ErrorIs(t, s.ReapplyApp(ctx, 1), proxy.ErrInactive, string(state))
	}
}
