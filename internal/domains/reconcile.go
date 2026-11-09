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
// proxy rodando (active ou trial) e fora de sincronia, recarrega os sites no
// modo de escuta do status, com as faixas da Cloudflare atuais. Devolve o
// status.
//
// Pausado (migração em andamento), não faz nada e devolve o último status.
func (s *Service) Reconcile(ctx context.Context) proxy.Status {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	if s.paused.Load() {
		return s.Status()
	}
	st := s.Proxy.Ensure(ctx)
	s.setStatus(st)
	if (st.State != proxy.StateActive && st.State != proxy.StateTrial) || ctx.Err() != nil {
		return st
	}
	mode := st.Mode
	if mode == "" {
		mode = proxy.ListenNormal
		if st.State == proxy.StateTrial {
			mode = proxy.ListenTrial
		}
	}

	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	sites, err := s.Sites(ctx)
	if err != nil {
		log.Printf("proxy: %v", err)
		return st
	}
	cf := s.cfRanges()
	ok, err := s.Proxy.InSync(ctx, sites, mode, cf)
	if err != nil {
		log.Printf("proxy: comparar configuração: %v", err)
	}
	if ok && err == nil {
		return st
	}
	if err := s.Proxy.Apply(ctx, sites, mode, cf); err != nil {
		log.Printf("proxy: reaplicar configuração: %v", err)
		return st
	}
	log.Printf("proxy: configuração fora de sincronia reaplicada (%d sites, modo %s)", len(sites), mode)
	return st
}

// Pause para a reconciliação e os deploys durante a migração do proxy:
// espera a volta de Reconcile em andamento, faz as próximas não fazerem nada
// (nem Ensure, que mudaria o estado do proxy no meio da troca) e prende
// applyMu, então Reapply e ReapplyApp esperam a retomada. Quem pausa usa
// Sites e CFRanges (que não tomam applyMu) e chama Resume uma única vez.
func (s *Service) Pause() {
	s.reconcileMu.Lock()
	s.paused.Store(true)
	s.reconcileMu.Unlock()
	s.applyMu.Lock()
}

// Resume desfaz Pause. Com st.State preenchido, publica st como o status
// atual antes de soltar os deploys, que aplicam no modo dele.
func (s *Service) Resume(st proxy.Status) {
	if st.State != "" {
		s.setStatus(st)
	}
	s.paused.Store(false)
	s.applyMu.Unlock()
}

// Paused diz se a reconciliação está pausada por uma migração.
func (s *Service) Paused() bool { return s.paused.Load() }

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
