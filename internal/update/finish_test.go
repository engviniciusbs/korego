package update_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/update"
)

func TestFinish_HealthyNoRollback(t *testing.T) {
	exe := newExe(t)
	restarts, checks := 0, 0
	f := &update.Finisher{
		ExePath: exe, Timeout: time.Second, Interval: time.Millisecond,
		Restart: func(context.Context) error { restarts++; return nil },
		Health: func(context.Context) error {
			checks++
			if checks < 3 {
				return errors.New("ainda subindo")
			}
			return nil
		},
	}
	require.NoError(t, f.Run(context.Background()))
	assert.Equal(t, 1, restarts)
	got, _ := os.ReadFile(exe)
	assert.Equal(t, "binario-antigo", string(got))
}

func TestFinish_UnhealthyRollsBack(t *testing.T) {
	exe := newExe(t) // representa o binário novo (que falha)
	require.NoError(t, os.WriteFile(exe+".prev", []byte("binario-anterior"), 0o755))
	restarts := 0
	f := &update.Finisher{
		ExePath: exe, Timeout: 30 * time.Millisecond, Interval: time.Millisecond,
		Restart: func(context.Context) error { restarts++; return nil },
		Health:  func(context.Context) error { return errors.New("fora do ar") },
	}
	err := f.Run(context.Background())
	require.ErrorIs(t, err, update.ErrRolledBack)
	assert.Equal(t, 2, restarts)
	got, _ := os.ReadFile(exe)
	assert.Equal(t, "binario-anterior", string(got))
	failed, _ := os.ReadFile(exe + ".failed")
	assert.Equal(t, "binario-antigo", string(failed))
}
