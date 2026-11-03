package auth

import (
	"sync"
	"time"
)

type limitEntry struct {
	fails        []time.Time
	blockedUntil time.Time
}

// Limiter blocks an IP for a while after too many failures within a window.
// It is safe for concurrent use.
type Limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	block  time.Duration
	m      map[string]*limitEntry
}

// NewLimiter blocks an IP for block after max failures within window.
func NewLimiter(max int, window, block time.Duration) *Limiter {
	return &Limiter{max: max, window: window, block: block, m: map[string]*limitEntry{}}
}

// Blocked reports whether ip is currently blocked.
func (l *Limiter) Blocked(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.m[ip]
	return ok && now.Before(e.blockedUntil)
}

// Fail records a failure for ip and drops stale entries.
func (l *Limiter) Fail(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(now)
	e := l.m[ip]
	if e == nil {
		e = &limitEntry{}
		l.m[ip] = e
	}
	e.fails = append(e.fails, now)
	if len(e.fails) >= l.max {
		e.blockedUntil = now.Add(l.block)
		e.fails = nil
	}
}

// Reset clears all state for ip.
func (l *Limiter) Reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, ip)
}

// prune removes failures outside the window and entries with nothing left.
func (l *Limiter) prune(now time.Time) {
	for ip, e := range l.m {
		kept := e.fails[:0]
		for _, t := range e.fails {
			if now.Sub(t) < l.window {
				kept = append(kept, t)
			}
		}
		e.fails = kept
		if len(e.fails) == 0 && !now.Before(e.blockedUntil) {
			delete(l.m, ip)
		}
	}
}
