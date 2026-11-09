package certs_test

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/certs"
)

func TestService_AddFromPath_FIFONoHang(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fifo.pem")
	require.NoError(t, syscall.Mkfifo(p, 0o600))
	done := make(chan error, 1)
	go func() {
		_, err := newSvc(t).AddFromPath(context.Background(), "x", p)
		done <- err
	}()
	select {
	case err := <-done:
		require.ErrorIs(t, err, certs.ErrInvalidCert)
		assert.Equal(t, readFail, err.Error())
	case <-time.After(5 * time.Second):
		t.Fatal("AddFromPath travou num FIFO")
	}
}
