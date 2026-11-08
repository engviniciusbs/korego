// Package jobs implementa uma fila de tarefas com concorrência limitada e, por
// chave, no máximo um job rodando e um esperando. Um novo job da mesma chave
// substitui o que espera.
package jobs

import (
	"context"
	"log"
	"sync"
)

// Job é uma unidade de trabalho enfileirada.
type Job struct {
	Key string // ex.: "app:12"
	ID  int64  // ex.: deploy id
	Run func(ctx context.Context)
	// OnSuperseded é chamado quando outro Job da mesma chave toma a vaga de
	// espera, ou quando o Job é enfileirado depois do Close.
	OnSuperseded func()
}

type running struct {
	key    string
	cancel context.CancelFunc
}

// Queue executa Jobs com limite de concorrência.
type Queue struct {
	mu          sync.Mutex
	concurrency int
	waiting     []Job // ordem de chegada; no máximo um por chave
	running     map[int64]running
	busyKeys    map[string]bool
	closed      bool
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

// NewQueue cria a fila. concurrency <= 0 usa 2.
func NewQueue(concurrency int) *Queue {
	if concurrency <= 0 {
		concurrency = 2
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Queue{
		concurrency: concurrency,
		running:     map[int64]running{},
		busyKeys:    map[string]bool{},
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Enqueue coloca o job na fila. Se já houver um job esperando na mesma chave,
// ele é substituído (e seu OnSuperseded é chamado, fora do lock). Depois do
// Close, o job não roda e recebe OnSuperseded.
func (q *Queue) Enqueue(j Job) {
	var superseded func()

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		if j.OnSuperseded != nil {
			j.OnSuperseded()
		}
		return
	}
	replaced := false
	for i := range q.waiting {
		if q.waiting[i].Key == j.Key {
			superseded = q.waiting[i].OnSuperseded
			q.waiting[i] = j // mantém a posição de chegada
			replaced = true
			break
		}
	}
	if !replaced {
		q.waiting = append(q.waiting, j)
	}
	q.dispatchLocked()
	q.mu.Unlock()

	if superseded != nil {
		superseded()
	}
}

// Cancel remove um job em espera (true, sem chamar OnSuperseded) ou cancela o
// ctx de um job rodando (true). Devolve false se o id não for conhecido.
func (q *Queue) Cancel(id int64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.waiting {
		if q.waiting[i].ID == id {
			q.waiting = append(q.waiting[:i], q.waiting[i+1:]...)
			return true
		}
	}
	if r, ok := q.running[id]; ok {
		r.cancel()
		return true
	}
	return false
}

// Close cancela os jobs que rodam, descarta os que esperam (sem OnSuperseded)
// e espera os que rodam terminarem. Pode ser chamado mais de uma vez.
func (q *Queue) Close() {
	q.mu.Lock()
	q.closed = true
	q.waiting = nil
	q.mu.Unlock()
	q.cancel()
	q.wg.Wait()
}

// dispatchLocked inicia, enquanto houver vaga, o job em espera mais antigo cuja
// chave não esteja rodando. Exige q.mu travado.
func (q *Queue) dispatchLocked() {
	for len(q.running) < q.concurrency {
		idx := -1
		for i, w := range q.waiting {
			if !q.busyKeys[w.Key] {
				idx = i
				break
			}
		}
		if idx < 0 {
			return
		}
		j := q.waiting[idx]
		q.waiting = append(q.waiting[:idx], q.waiting[idx+1:]...)

		ctx, cancel := context.WithCancel(q.ctx)
		q.running[j.ID] = running{key: j.Key, cancel: cancel}
		q.busyKeys[j.Key] = true
		q.wg.Add(1)
		go q.run(ctx, cancel, j)
	}
}

func (q *Queue) run(ctx context.Context, cancel context.CancelFunc, j Job) {
	defer q.wg.Done()
	defer func() {
		cancel()
		q.mu.Lock()
		delete(q.running, j.ID)
		delete(q.busyKeys, j.Key)
		if !q.closed {
			q.dispatchLocked()
		}
		q.mu.Unlock()
	}()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("jobs: job %d (%s) entrou em panic: %v", j.ID, j.Key, r)
		}
	}()
	j.Run(ctx)
}
