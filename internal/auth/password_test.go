package auth

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHashVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$"))

	ok, err := VerifyPassword(h, "correct horse battery")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = VerifyPassword(h, "wrong password!!")
	require.NoError(t, err)
	require.False(t, ok)

	_, err = VerifyPassword("lixo", "x")
	require.Error(t, err)
}

func TestPolicy(t *testing.T) {
	require.ErrorIs(t, ValidatePasswordPolicy("curta"), ErrWeakPassword)
	require.NoError(t, ValidatePasswordPolicy("123456789012"))
}

// trackArgon installs a hook that records the peak number of concurrent
// argon2 computations and the total number of calls.
func trackArgon(t *testing.T, hold time.Duration) (peak, calls *atomic.Int32) {
	t.Helper()
	var active atomic.Int32
	peak, calls = new(atomic.Int32), new(atomic.Int32)
	argonHook = func() {
		calls.Add(1)
		n := active.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(hold)
		active.Add(-1)
	}
	t.Cleanup(func() { argonHook = nil })
	return peak, calls
}

func TestArgonSemaphore_MaxTwo(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	require.NoError(t, err)
	peak, calls := trackArgon(t, 20*time.Millisecond)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := VerifyPasswordCtx(context.Background(), h, "correct horse battery")
			require.NoError(t, err)
			require.True(t, ok)
		}()
	}
	wg.Wait()
	require.EqualValues(t, 8, calls.Load())
	require.EqualValues(t, 2, peak.Load())
}

func TestArgonSemaphore_Busy(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	argonHook = func() { started <- struct{}{}; <-release }
	oldWait := argonWait
	argonWait = 30 * time.Millisecond
	t.Cleanup(func() { argonHook = nil; argonWait = oldWait })

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = HashPasswordCtx(context.Background(), "correct horse battery")
		}()
	}
	<-started
	<-started
	_, err := HashPasswordCtx(context.Background(), "correct horse battery")
	require.ErrorIs(t, err, ErrBusy)
	close(release)
	wg.Wait()
}
