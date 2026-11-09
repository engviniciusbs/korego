//go:build !linux

package proxy

// ensureWWWDir não faz nada fora de Linux (o Caddy só roda em Linux).
func ensureWWWDir() error { return nil }
