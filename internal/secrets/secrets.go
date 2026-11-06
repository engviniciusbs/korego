// Package secrets gerencia a chave mestra e a criptografia AES-256-GCM de segredos.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const keySize = 32

// ErrKeyMissing indica que a chave mestra não existe e a criação não foi permitida.
var ErrKeyMissing = errors.New("secrets: chave mestra ausente")

// LoadOrCreateKey lê a chave mestra de path. Se o arquivo não existir e allowCreate
// for verdadeiro, gera 32 bytes aleatórios e grava com permissão 0600 (de forma
// atômica e sem sobrescrever um arquivo criado concorrentemente).
func LoadOrCreateKey(path string, allowCreate bool) ([]byte, error) {
	key, err := readKey(path)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if !allowCreate {
		return nil, fmt.Errorf("%w: %s", ErrKeyMissing, path)
	}
	if err := createKey(path); err != nil {
		return nil, err
	}
	return readKey(path)
}

func readKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) != keySize {
		return nil, fmt.Errorf("secrets: chave mestra inválida em %s: %d bytes (esperado %d)", path, len(data), keySize)
	}
	return data, nil
}

func createKey(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("secrets: criar diretório da chave: %w", err)
	}
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("secrets: gerar chave: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".master.key.*")
	if err != nil {
		return fmt.Errorf("secrets: arquivo temporário: %w", err)
	}
	defer os.Remove(tmp.Name())
	// os.CreateTemp já cria o arquivo com 0600.
	if _, err := tmp.Write(key); err != nil {
		tmp.Close()
		return fmt.Errorf("secrets: gravar chave: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("secrets: sincronizar chave: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("secrets: fechar chave: %w", err)
	}
	// Link falha se o destino já existe: quem chegou primeiro vence, sem sobrescrever.
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("secrets: publicar chave: %w", err)
	}
	return nil
}

// Box cifra e decifra segredos com AES-256-GCM.
type Box struct {
	aead cipher.AEAD
}

// NewBox cria um Box a partir de uma chave de 32 bytes.
func NewBox(key []byte) (*Box, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("secrets: chave deve ter %d bytes, recebeu %d", keySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Encrypt devolve base64url(nonce || ciphertext).
func (b *Box) Encrypt(plain []byte) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secrets: gerar nonce: %w", err)
	}
	out := b.aead.Seal(nonce, nonce, plain, nil)
	return base64.RawURLEncoding.EncodeToString(out), nil
}

// Decrypt reverte Encrypt; entrada malformada ou adulterada devolve erro.
func (b *Box) Decrypt(s string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("secrets: base64 inválido: %w", err)
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns+b.aead.Overhead() {
		return nil, errors.New("secrets: dado cifrado curto demais")
	}
	plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return nil, fmt.Errorf("secrets: falha ao decifrar: %w", err)
	}
	return plain, nil
}
