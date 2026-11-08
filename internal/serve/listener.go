// Package serve tem o listener HTTP do painel, que troca de endereço sem
// reiniciar o servidor (porta pública ligada ou só em 127.0.0.1).
package serve

import (
	"errors"
	"fmt"
	"net"
	"sync"
)

// Listener é um net.Listener cujo endereço pode ser trocado com Switch. O
// http.Server continua no mesmo Listener: o Accept em andamento durante a
// troca passa para o listener novo em vez de devolver erro, e as conexões já
// aceitas seguem abertas. Accept só devolve net.ErrClosed depois de Close.
type Listener struct {
	switchMu sync.Mutex // serializa Switch

	mu        sync.Mutex
	cond      *sync.Cond
	cur       net.Listener
	addr      string // endereço pedido do listener atual
	gen       uint64 // muda a cada troca
	switching bool
	closed    bool
	broken    error // a troca falhou e nem o endereço antigo reabriu
}

var _ net.Listener = (*Listener)(nil)

// NewListener abre addr (TCP).
func NewListener(addr string) (*Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	l := &Listener{cur: ln, addr: addr}
	l.cond = sync.NewCond(&l.mu)
	return l, nil
}

// Accept espera a próxima conexão no listener atual.
func (l *Listener) Accept() (net.Conn, error) {
	for {
		l.mu.Lock()
		for l.switching && !l.closed {
			l.cond.Wait()
		}
		if l.closed {
			l.mu.Unlock()
			return nil, net.ErrClosed
		}
		if l.broken != nil {
			err := l.broken
			l.mu.Unlock()
			return nil, err
		}
		ln, gen := l.cur, l.gen
		l.mu.Unlock()

		c, err := ln.Accept()
		if err == nil {
			return c, nil
		}
		l.mu.Lock()
		retry := !l.closed && (l.switching || l.gen != gen)
		closed := l.closed
		l.mu.Unlock()
		switch {
		case closed:
			return nil, net.ErrClosed
		case retry:
			continue // o listener foi trocado: espera no novo
		default:
			return nil, err
		}
	}
}

// Close fecha o listener; Accept passa a devolver net.ErrClosed.
func (l *Listener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	l.cond.Broadcast()
	if l.switching || l.broken != nil {
		return nil // Switch fecha o listener novo ao terminar
	}
	return l.cur.Close()
}

// Addr devolve o endereço do listener atual.
func (l *Listener) Addr() net.Addr {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cur.Addr()
}

// Switch passa a escutar em addr. Fecha o listener antigo antes de abrir o
// novo (os dois podem usar a mesma porta, como :8000 e 127.0.0.1:8000). Se
// abrir addr falhar, reabre o endereço antigo e devolve o erro. addr igual ao
// atual não faz nada.
func (l *Listener) Switch(addr string) error {
	l.switchMu.Lock()
	defer l.switchMu.Unlock()

	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return net.ErrClosed
	}
	if l.broken == nil && addr == l.addr {
		l.mu.Unlock()
		return nil
	}
	old, oldAddr, wasBroken := l.cur, l.addr, l.broken != nil
	reopen := reopenAddr(oldAddr, old.Addr())
	l.switching = true
	l.mu.Unlock()

	if !wasBroken {
		_ = old.Close()
	}
	ln, err := net.Listen("tcp", addr)
	newAddr := addr
	var broken error
	if err != nil {
		var rerr error
		ln, rerr = net.Listen("tcp", reopen)
		newAddr = reopen
		if rerr != nil {
			err = errors.Join(err, fmt.Errorf("reabrir %s: %w", reopen, rerr))
			broken = err
			ln = old // mantém um Addr válido; Accept devolve broken
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.cur, l.addr, l.broken = ln, newAddr, broken
	l.gen++
	l.switching = false
	l.cond.Broadcast()
	if l.closed && broken == nil {
		_ = ln.Close()
	}
	return err
}

// reopenAddr é o endereço para reabrir o listener antigo: o pedido, salvo
// porta 0 (aí o endereço efetivo, para manter a mesma porta).
func reopenAddr(requested string, bound net.Addr) string {
	if _, port, err := net.SplitHostPort(requested); err == nil && port != "0" {
		return requested
	}
	return bound.String()
}
