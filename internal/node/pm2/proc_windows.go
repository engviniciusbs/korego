//go:build windows

package pm2

import "os"

// processExists é best-effort: o Windows não é alvo do PM2 do KoreGO.
func processExists(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
