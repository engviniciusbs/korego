package api_test

import (
	"runtime"
	"testing"
)

// linuxOnly pula testes que dependem de caminhos e permissões do Linux.
func linuxOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("só Linux: caminhos e permissões POSIX")
	}
}
