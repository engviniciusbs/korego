//go:build !windows

package pm2

import (
	"errors"
	"syscall"
)

// processExists usa kill(pid, 0): ESRCH = não existe; EPERM = existe.
func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
