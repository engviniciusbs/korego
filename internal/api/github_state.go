package api

import (
	"container/list"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

const (
	// stateTTL é a validade do state do fluxo de manifesto (D7).
	stateTTL = 10 * time.Minute
	// maxStates limita os states pendentes; cheio, o mais antigo sai.
	maxStates = 100
	// maxDeliveries é quantas entregas de webhook ficam lembradas.
	maxDeliveries = 1000
)

type stateEntry struct {
	userID  int64
	created time.Time
}

// stateStore guarda em memória os states do fluxo de manifesto: aleatórios,
// de uso único e com validade. O callback do GitHub chega sem o cookie de
// sessão (SameSite=Strict), então o state é a autenticação dele.
type stateStore struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]stateEntry
}

func newStateStore(now func() time.Time) *stateStore {
	if now == nil {
		now = time.Now
	}
	return &stateStore{now: now, m: map[string]stateEntry{}}
}

// issue cria um state para o usuário.
func (s *stateStore) issue(userID int64) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	st := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, e := range s.m {
		if !now.Before(e.created.Add(stateTTL)) {
			delete(s.m, k)
		}
	}
	for len(s.m) >= maxStates {
		oldest, first := "", true
		var at time.Time
		for k, e := range s.m {
			if first || e.created.Before(at) {
				oldest, at, first = k, e.created, false
			}
		}
		delete(s.m, oldest)
	}
	s.m[st] = stateEntry{userID: userID, created: now}
	return st, nil
}

// take consome o state: some na primeira consulta, válido ou não.
func (s *stateStore) take(st string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[st]
	if !ok {
		return 0, false
	}
	delete(s.m, st)
	if !s.now().Before(e.created.Add(stateTTL)) {
		return 0, false
	}
	return e.userID, true
}

func (s *stateStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}

// deliverySet lembra os últimos X-GitHub-Delivery (LRU) para descartar repetidos.
type deliverySet struct {
	mu  sync.Mutex
	max int
	m   map[string]*list.Element
	l   *list.List
}

func newDeliverySet(max int) *deliverySet {
	return &deliverySet{max: max, m: map[string]*list.Element{}, l: list.New()}
}

// seen registra id e diz se ele já tinha sido visto. Id vazio nunca é repetido.
func (d *deliverySet) seen(id string) bool {
	if id == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if e, ok := d.m[id]; ok {
		d.l.MoveToFront(e)
		return true
	}
	d.m[id] = d.l.PushFront(id)
	for d.l.Len() > d.max {
		old := d.l.Back()
		d.l.Remove(old)
		delete(d.m, old.Value.(string))
	}
	return false
}

// forget esquece id (a entrega falhou e pode ser reenviada).
func (d *deliverySet) forget(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if e, ok := d.m[id]; ok {
		d.l.Remove(e)
		delete(d.m, id)
	}
}
