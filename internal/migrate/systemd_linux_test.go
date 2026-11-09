//go:build linux

package migrate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSystemctl põe um "systemctl" de mentira no PATH.
func fakeSystemctl(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "systemctl"), []byte("#!/bin/sh\n"+script+"\n"), 0o755))
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

func TestSystemd_ActiveStates(t *testing.T) {
	fakeSystemctl(t, `if [ "$2" = "nginx.service" ]; then echo active; exit 0; fi; echo inactive; exit 3`)
	ok, err := Systemd{}.Active(context.Background(), "nginx")
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = Systemd{}.Active(context.Background(), "apache2")
	require.NoError(t, err, "inactive (saída 3) não é erro")
	assert.False(t, ok)
}

func TestSystemd_TimeoutIsErrorNotInactive(t *testing.T) {
	fakeSystemctl(t, `sleep 5`)
	old := systemctlTimeout
	systemctlTimeout = 100 * time.Millisecond
	t.Cleanup(func() { systemctlTimeout = old })
	ok, err := Systemd{}.Active(context.Background(), "nginx")
	assert.False(t, ok)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tempo esgotado")
	assert.Error(t, Systemd{}.Stop(context.Background(), "nginx"))
}

func TestSystemd_EnabledUnit(t *testing.T) {
	fakeSystemctl(t, `if [ "$1" = "is-enabled" ] && [ "$2" = "apache2.service" ]; then echo enabled; exit 0; fi; echo disabled; exit 1`)
	assert.Equal(t, "apache2", Systemd{}.EnabledUnit(context.Background()))
	fakeSystemctl(t, `echo disabled; exit 1`)
	assert.Equal(t, "", Systemd{}.EnabledUnit(context.Background()))
	fakeSystemctl(t, `if [ "$2" = "nginx.service" ]; then echo enabled; exit 0; fi; echo enabled; exit 0`)
	assert.Equal(t, "nginx", Systemd{}.EnabledUnit(context.Background()), "nginx primeiro")
}
