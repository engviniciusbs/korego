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

func TestPause_ReconcileSkipsEnsureAndApply(t *testing.T) {
	ctx := context.Background()
	s, fake := newTestService(t)
	var ensures atomic.Int32
	fake.EnsureFn = func(context.Context) proxy.Status {
		ensures.Add(1)
		return proxy.Status{State: proxy.StateActive, Mode: proxy.ListenNormal}
	}
	fake.InSyncFn = func(context.Context, []proxy.Site, proxy.Listen, []string) (bool, error) { return false, nil }
	s.Reconcile(ctx)
	require.EqualValues(t, 1, ensures.Load())
	applies := fake.Applies()

	s.Pause()
	st := s.Reconcile(ctx)
	assert.EqualValues(t, 1, ensures.Load(), "pausado: Ensure não roda")
	assert.Equal(t, applies, fake.Applies(), "pausado: Apply não roda")
	assert.Equal(t, proxy.StateActive, st.State, "devolve o último status")
	assert.True(t, s.Paused())

	// Reapply (deploys, edições) espera a retomada em vez de aplicar.
	done := make(chan error, 1)
	go func() { done <- s.Reapply(ctx) }()
	select {
	case <-done:
		t.Fatal("Reapply não deveria aplicar durante a migração")
	case <-time.After(50 * time.Millisecond):
	}
	assert.Equal(t, applies, fake.Applies())

	// Resume publica o status novo antes de liberar o Reapply, que usa o modo dele.
	s.Resume(proxy.Status{State: proxy.StateTrial, Mode: proxy.ListenTrial, Process: "nginx (pid 7)"})
	require.NoError(t, <-done)
	assert.Equal(t, proxy.ListenTrial, fake.LastMode())
	assert.Equal(t, proxy.StateTrial, s.Status().State)
	assert.False(t, s.Paused())

	s.Reconcile(ctx)
	assert.EqualValues(t, 2, ensures.Load())
}

func TestPause_WaitsForInFlightReconcile(t *testing.T) {
	ctx := context.Background()
	s, fake := newTestService(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	fake.EnsureFn = func(context.Context) proxy.Status {
		close(entered)
		<-release
		return proxy.Status{State: proxy.StateActive, Mode: proxy.ListenNormal}
	}
	go s.Reconcile(ctx)
	<-entered
	paused := make(chan struct{})
	go func() { s.Pause(); close(paused) }()
	select {
	case <-paused:
		t.Fatal("Pause não pode voltar com uma reconciliação em andamento")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-paused
	s.Resume(proxy.Status{})
	// Status vazio em Resume mantém o último status.
	assert.Equal(t, proxy.StateActive, s.Status().State)
}

func TestCFRanges_Exported(t *testing.T) {
	s, _ := newTestService(t)
	assert.Nil(t, s.CFRanges())
	s.Ranges = fixedRanges{"203.0.113.0/24"}
	assert.Equal(t, []string{"203.0.113.0/24"}, s.CFRanges())
}
