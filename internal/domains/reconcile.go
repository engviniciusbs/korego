package domains

import (
	"context"
	"log"
	"time"

	"github.com/engviniciusbs/korego/internal/node/proxy"
)

// DefaultInterval é o intervalo padrão da reconciliação.
const DefaultInterval = 30 * time.Second

// TopicProxy é o tópico do hub em que as mudanças de Status são publicadas.
const TopicProxy = "proxy"

// Run reconcilia o proxy na hora e depois a cada Interval, até ctx acabar.
func (s *Service) Run(ctx context.Context) {
	iv := s.Interval
	if iv <= 0 {
		iv = DefaultInterval
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		s.Reconcile(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Reconcile faz uma volta: Proxy.Ensure, publica o status se mudou e, com o
// proxy ativo e fora de sincronia, recarrega as rotas. Devolve o status.
func (s *Service) Reconcile(ctx context.Context) proxy.Status {
	st := s.Proxy.Ensure(ctx)
	s.setStatus(st)
	if st.State != proxy.StateActive || ctx.Err() != nil {
		return st
	}

	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	routes, err := s.Routes(ctx)
	if err != nil {
		log.Printf("proxy: %v", err)
		return st
	}
	ok, err := s.Proxy.InSync(ctx, routes)
	if err != nil {
		log.Printf("proxy: comparar configuração: %v", err)
	}
	if ok && err == nil {
		return st
	}
	if err := s.Proxy.Apply(ctx, routes); err != nil {
		log.Printf("proxy: reaplicar configuração: %v", err)
		return st
	}
	log.Printf("proxy: configuração fora de sincronia reaplicada (%d rotas)", len(routes))
	return st
}

// Status devolve o último status do proxy visto pela reconciliação.
func (s *Service) Status() proxy.Status {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	return s.status
}

func (s *Service) setStatus(st proxy.Status) {
	// Publish nunca bloqueia; publicar sob o lock mantém a ordem dos eventos.
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	changed := !s.hasStatus || s.status != st
	s.status, s.hasStatus = st, true
	if st.State != proxy.StateBlocked {
		s.blockedLogged = false
	}
	if changed && s.Hub != nil {
		s.Hub.Publish(TopicProxy, st)
	}
}
