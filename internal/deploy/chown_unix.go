//go:build !windows

package deploy

import (
	"os"
	"syscall"
)

// chownLike copia dono e grupo de fi para name; erros (EPERM sem root) são ignorados.
func chownLike(name string, fi os.FileInfo) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		_ = os.Chown(name, int(st.Uid), int(st.Gid))
	}
}
