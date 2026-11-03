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

func TestFinish_RestartErrorRollsBack(t *testing.T) {
	exe := newExe(t)
	require.NoError(t, os.WriteFile(exe+".prev", []byte("binario-anterior"), 0o755))
	calls := 0
	f := &update.Finisher{
		ExePath: exe, Timeout: 10 * time.Millisecond, Interval: time.Millisecond,
		Restart: func(context.Context) error {
			calls++
			if calls == 1 {
				return errors.New("systemctl falhou")
			}
			return nil
		},
		Health: func(context.Context) error { return nil },
	}
	require.ErrorIs(t, f.Run(context.Background()), update.ErrRolledBack)
	assert.Equal(t, 2, calls)
	got, _ := os.ReadFile(exe)
	assert.Equal(t, "binario-anterior", string(got))
}

func TestFinish_NoPrevKeepsExe(t *testing.T) {
	exe := newExe(t)
	f := &update.Finisher{
		ExePath: exe, Timeout: 10 * time.Millisecond, Interval: time.Millisecond,
		Restart: func(context.Context) error { return nil },
		Health:  func(context.Context) error { return errors.New("fora do ar") },
	}
	err := f.Run(context.Background())
	require.Error(t, err)
	assert.NotErrorIs(t, err, update.ErrRolledBack)
	got, _ := os.ReadFile(exe)
	assert.Equal(t, "binario-antigo", string(got))
	assert.NoFileExists(t, exe+".failed")
}

func TestSystem_StartFinishArgv(t *testing.T) {
	var name string
	var args []string
	s := &update.System{
		ExePath: "/usr/local/bin/korego", ConfigPath: "/etc/korego/config.toml",
		Run: func(n string, a ...string) ([]byte, error) { name, args = n, a; return nil, nil },
	}
	require.NoError(t, s.StartFinish())
	assert.Equal(t, "systemd-run", name)
	assert.Equal(t, []string{
		"--unit=korego-update", "--collect", "/usr/local/bin/korego.prev", "update-finish",
		"--exe", "/usr/local/bin/korego", "--config", "/etc/korego/config.toml",
	}, args)

	s.Run = func(string, ...string) ([]byte, error) { return []byte("unit ja existe"), errors.New("exit 1") }
	err := s.StartFinish()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unit ja existe")
}

func TestRestore(t *testing.T) {
	exe := newExe(t)
	require.Error(t, update.Restore(exe))
	got, _ := os.ReadFile(exe)
	assert.Equal(t, "binario-antigo", string(got))

	require.NoError(t, os.WriteFile(exe+".prev", []byte("binario-anterior"), 0o755))
	require.NoError(t, update.Restore(exe))
	got, _ = os.ReadFile(exe)
	assert.Equal(t, "binario-anterior", string(got))
	assert.NoFileExists(t, exe+".prev")
}
