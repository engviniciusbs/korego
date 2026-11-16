package deploy

import (
	"runtime"
	"testing"
)

// linuxOnly pula testes que dependem de links simbólicos, permissões POSIX e
// caminhos do Linux: versões estáticas e .env só existem no servidor Linux.
func linuxOnly(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("só Linux: links simbólicos e permissões POSIX")
	}
}
