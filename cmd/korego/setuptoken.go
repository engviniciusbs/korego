package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// loadOrCreateSetupToken returns the one-time setup token stored at path,
// creating it (32 random bytes, base64url, mode 0600) if it does not exist.
// Creation is atomic and never overwrites a file created concurrently.
func loadOrCreateSetupToken(path string) (string, error) {
	tok, err := readSetupToken(path)
	if err == nil || !errors.Is(err, fs.ErrNotExist) {
		return tok, err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("criar diretório do token de setup: %w", err)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("gerar token de setup: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".setup-token.*") // created with 0600
	if err != nil {
		return "", fmt.Errorf("arquivo temporário do token de setup: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(base64.RawURLEncoding.EncodeToString(raw))
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("gravar token de setup: %w", err)
	}
	// Link fails if path already exists: the first writer wins.
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("publicar token de setup: %w", err)
	}
	return readSetupToken(path)
}

func readSetupToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(data))
	if tok == "" {
		return "", fmt.Errorf("token de setup vazio em %s; apague o arquivo para gerar outro", path)
	}
	return tok, nil
}
