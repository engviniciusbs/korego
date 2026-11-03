package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLimiter_BlocksAfterFive(t *testing.T) {
	l := NewLimiter(5, time.Minute, 15*time.Minute)
	t0 := time.Unix(1_800_000_000, 0)
	for i := 0; i < 5; i++ {
		require.False(t, l.Blocked("203.0.113.10", t0.Add(time.Duration(i)*time.Second)))
		l.Fail("203.0.113.10", t0.Add(time.Duration(i)*time.Second))
	}
	require.True(t, l.Blocked("203.0.113.10", t0.Add(5*time.Second)))
	require.False(t, l.Blocked("203.0.113.11", t0.Add(5*time.Second)))

	l2 := NewLimiter(5, time.Minute, 15*time.Minute)
	for i := 0; i < 5; i++ {
		l2.Fail("203.0.113.10", t0.Add(time.Duration(i)*61*time.Second))
	}
	require.False(t, l2.Blocked("203.0.113.10", t0.Add(5*61*time.Second)))
}

func TestLimiter_BlockLasts15Min(t *testing.T) {
	l := NewLimiter(5, time.Minute, 15*time.Minute)
	t0 := time.Unix(1_800_000_000, 0)
	for i := 0; i < 5; i++ {
		l.Fail("203.0.113.10", t0)
	}
	require.True(t, l.Blocked("203.0.113.10", t0))
	require.True(t, l.Blocked("203.0.113.10", t0.Add(14*time.Minute+59*time.Second)))
	require.False(t, l.Blocked("203.0.113.10", t0.Add(15*time.Minute+time.Second)))
}

func TestLimiter_Reset(t *testing.T) {
	l := NewLimiter(2, time.Minute, time.Minute)
	t0 := time.Unix(1_800_000_000, 0)
	l.Fail("203.0.113.10", t0)
	l.Fail("203.0.113.10", t0)
	require.True(t, l.Blocked("203.0.113.10", t0))
	l.Reset("203.0.113.10")
	require.False(t, l.Blocked("203.0.113.10", t0))
}
