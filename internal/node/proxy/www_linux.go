//go:build linux

package proxy

import "os"

// ensureWWWDir cria /var/www (0755) se não existir; se já existe, não mexe
// nas permissões.
func ensureWWWDir() error {
	if _, err := os.Stat(wwwDir); err == nil {
		return nil
	}
	if err := os.MkdirAll(wwwDir, 0o755); err != nil {
		return err
	}
	return os.Chmod(wwwDir, 0o755)
}
