package jobs

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const wait = 2 * time.Second

// blocker é um Job controlável: sinaliza o início e só termina ao ser liberado
// (ou quando o ctx é cancelado).
type blocker struct {
	started chan struct{}
	release chan struct{}
	done    chan struct{}
	ctxErr  atomic.Value // error
}

func newBlocker() *blocker {
	return &blocker{
		started: make(chan struct{}),
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (b *blocker) run(ctx context.Context) {
	defer close(b.done)
	close(b.started)
	select {
	case <-b.release:
	case <-ctx.Done():
		b.ctxErr.Store(ctx.Err())
	}
}

func (b *blocker) job(key string, id int64) Job {
	return Job{Key: key, ID: id, Run: b.run}
}

func waitCh(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(wait):
		t.Fatalf("timeout esperando: %s", what)
	}
}

func notStarted(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("não deveria ter começado: %s", what)
	case <-time.After(10 * time.Millisecond):
	}
}

func TestQueue_ConcurrencyLimit(t *testing.T) {
	q := NewQueue(2)
	defer q.Close()

	var cur, max atomic.Int32
	bs := make([]*blocker, 5)
	for i := range bs {
		b := newBlocker()
		bs[i] = b
		q.Enqueue(Job{Key: "app:" + string(rune('a'+i)), ID: int64(i), Run: func(ctx context.Context) {
			n := cur.Add(1)
			for {
				m := max.Load()
				if n <= m || max.CompareAndSwap(m, n) {
					break
				}
			}
			b.run(ctx)
			cur.Add(-1)
		}})
	}

	waitCh(t, bs[0].started, "job 0")
	waitCh(t, bs[1].started, "job 1")
	notStarted(t, bs[2].started, "job 2 acima do limite")

	for i := 0; i < 5; i++ {
		waitCh(t, bs[i].started, "job")
		close(bs[i].release)
		waitCh(t, bs[i].done, "fim do job")
	}
	assert.LessOrEqual(t, max.Load(), int32(2))
}

func TestQueue_DefaultConcurrency(t *testing.T) {
	q := NewQueue(0)
	defer q.Close()
	a, b, c := newBlocker(), newBlocker(), newBlocker()
	q.Enqueue(a.job("a", 1))
	q.Enqueue(b.job("b", 2))
	q.Enqueue(c.job("c", 3))
	waitCh(t, a.started, "a")
	waitCh(t, b.started, "b")
	notStarted(t, c.started, "c")
	close(a.release)
	waitCh(t, c.started, "c após liberar vaga")
	close(b.release)
	close(c.release)
}

func TestQueue_OnePerKey(t *testing.T) {
	q := NewQueue(4)
	defer q.Close()
	a, b := newBlocker(), newBlocker()
	q.Enqueue(a.job("app:1", 1))
	q.Enqueue(b.job("app:1", 2))

	waitCh(t, a.started, "a")
	notStarted(t, b.started, "b enquanto a roda na mesma chave")

	close(a.release)
	waitCh(t, b.started, "b depois de a terminar")
	close(b.release)
}

func TestQueue_SupersedesWaiting(t *testing.T) {
	q := NewQueue(4)
	defer q.Close()
	a, b, c := newBlocker(), newBlocker(), newBlocker()

	var superseded atomic.Int32
	q.Enqueue(a.job("app:1", 1))
	waitCh(t, a.started, "a")

	jb := b.job("app:1", 2)
	jb.OnSuperseded = func() { superseded.Add(1) }
	q.Enqueue(jb)
	assert.Equal(t, int32(0), superseded.Load())

	jc := c.job("app:1", 3)
	q.Enqueue(jc)
	assert.Equal(t, int32(1), superseded.Load())

	close(a.release)
	waitCh(t, c.started, "c depois de a")
	notStarted(t, b.started, "b substituído")
	close(c.release)
	assert.Equal(t, int32(1), superseded.Load())
}

func TestQueue_CancelWaitingAndRunning(t *testing.T) {
	q := NewQueue(4)
	defer q.Close()
	a, b := newBlocker(), newBlocker()

	var superseded atomic.Int32
	q.Enqueue(a.job("app:1", 1))
	waitCh(t, a.started, "a")
	jb := b.job("app:1", 2)
	jb.OnSuperseded = func() { superseded.Add(1) }
	q.Enqueue(jb)

	// em espera: removido, sem OnSuperseded
	assert.True(t, q.Cancel(2))
	assert.Equal(t, int32(0), superseded.Load())
	assert.False(t, q.Cancel(2))

	// rodando: ctx cancelado
	assert.True(t, q.Cancel(1))
	waitCh(t, a.done, "a cancelado")
	assert.ErrorIs(t, a.ctxErr.Load().(error), context.Canceled)

	// b nunca roda
	notStarted(t, b.started, "b cancelado")
	assert.False(t, q.Cancel(99))
}

func TestQueue_CloseCancelsAndWaits(t *testing.T) {
	q := NewQueue(1)
	a, b := newBlocker(), newBlocker()

	var superseded atomic.Int32
	q.Enqueue(a.job("app:1", 1))
	jb := b.job("app:2", 2)
	jb.OnSuperseded = func() { superseded.Add(1) }
	q.Enqueue(jb)
	waitCh(t, a.started, "a")

	closed := make(chan struct{})
	go func() { q.Close(); close(closed) }()
	waitCh(t, closed, "Close retornar")

	// Close só retorna depois do job que rodava terminar
	select {
	case <-a.done:
	default:
		t.Fatal("Close retornou antes de a terminar")
	}
	assert.ErrorIs(t, a.ctxErr.Load().(error), context.Canceled)
	notStarted(t, b.started, "b descartado")
	assert.Equal(t, int32(0), superseded.Load())

	// Enqueue após Close: não roda, avisa por OnSuperseded
	c := newBlocker()
	jc := c.job("app:3", 3)
	var cSuperseded atomic.Int32
	jc.OnSuperseded = func() { cSuperseded.Add(1) }
	q.Enqueue(jc)
	notStarted(t, c.started, "c após Close")
	assert.Equal(t, int32(1), cSuperseded.Load())

	q.Close() // idempotente
}

func TestQueue_FIFOSkipsBusyKeys(t *testing.T) {
	q := NewQueue(2)
	defer q.Close()
	a1, a2, b1 := newBlocker(), newBlocker(), newBlocker()

	q.Enqueue(a1.job("A", 1))
	waitCh(t, a1.started, "a1")
	q.Enqueue(a2.job("A", 2)) // espera: A ocupada
	q.Enqueue(b1.job("B", 3)) // chave livre, vaga livre: roda já
	waitCh(t, b1.started, "b1")
	notStarted(t, a2.started, "a2")

	// libera a1: a2 é a mais antiga e sua chave está livre
	close(a1.release)
	waitCh(t, a2.started, "a2")
	close(a2.release)
	close(b1.release)
}

func TestQueue_FIFOWhenSlotFrees(t *testing.T) {
	q := NewQueue(1)
	defer q.Close()
	var mu sync.Mutex
	var order []int64
	gate := newBlocker()
	q.Enqueue(gate.job("g", 0))
	waitCh(t, gate.started, "gate")

	dones := make([]chan struct{}, 3)
	for i := 0; i < 3; i++ {
		d := make(chan struct{})
		dones[i] = d
		id := int64(i + 1)
		q.Enqueue(Job{Key: string(rune('x' + i)), ID: id, Run: func(context.Context) {
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
			close(d)
		}})
	}
	close(gate.release)
	for _, d := range dones {
		waitCh(t, d, "job")
	}
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []int64{1, 2, 3}, order)
}

func TestQueue_PanicFreesSlot(t *testing.T) {
	q := NewQueue(1)
	defer q.Close()
	q.Enqueue(Job{Key: "A", ID: 1, Run: func(context.Context) { panic("boom") }})
	b := newBlocker()
	q.Enqueue(b.job("B", 2))
	waitCh(t, b.started, "b depois do panic")
	close(b.release)
}
