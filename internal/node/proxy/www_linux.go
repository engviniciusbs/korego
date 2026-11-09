//go:build linux

package proxy

import "os"

// ensureWWWDir cria /var/www (0755) se não existir.
func ensureWWWDir() error {
	if err := os.MkdirAll(wwwDir, 0o755); err != nil {
		return err
	}
	return os.Chmod(wwwDir, 0o755)
}
