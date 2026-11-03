// Package auth implements passwords, TOTP and recovery codes.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory  uint32 = 65536
	argonTime    uint32 = 3
	argonThreads uint8  = 2
	argonSaltLen        = 16
	argonKeyLen  uint32 = 32
	minPassword         = 12
)

// ErrWeakPassword is returned when a password violates the policy.
var ErrWeakPassword = errors.New("a senha deve ter pelo menos 12 caracteres")

// ErrBusy is returned when no argon2 slot frees up within argonWait.
var ErrBusy = errors.New("servidor ocupado")

// Each argon2id computation allocates argonMemory KiB (64 MiB), so at most
// two run at once, process-wide, to bound memory use under load.
var (
	argonSlots = make(chan struct{}, 2)
	argonWait  = 5 * time.Second
	argonHook  func() // test seam: runs while a slot is held
)

// withArgonSlot runs f while holding an argon2 slot, waiting at most argonWait.
func withArgonSlot(ctx context.Context, f func()) error {
	ctx, cancel := context.WithTimeout(ctx, argonWait)
	defer cancel()
	select {
	case argonSlots <- struct{}{}:
	case <-ctx.Done():
		return ErrBusy
	}
	defer func() { <-argonSlots }()
	if argonHook != nil {
		argonHook()
	}
	f()
	return nil
}

// ValidatePasswordPolicy enforces the minimum password length.
func ValidatePasswordPolicy(pw string) error {
	if utf8.RuneCountInString(pw) < minPassword {
		return ErrWeakPassword
	}
	return nil
}

// HashPassword returns a PHC-formatted argon2id hash.
func HashPassword(pw string) (string, error) {
	return HashPasswordCtx(context.Background(), pw)
}

// HashPasswordCtx is HashPassword bounded by the argon2 semaphore; it returns
// ErrBusy if no slot frees up in time.
func HashPasswordCtx(ctx context.Context, pw string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("gerar salt: %w", err)
	}
	var key []byte
	if err := withArgonSlot(ctx, func() {
		key = argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version,
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks pw against an encoded hash in constant time.
func VerifyPassword(encoded, pw string) (bool, error) {
	return VerifyPasswordCtx(context.Background(), encoded, pw)
}

// VerifyPasswordCtx is VerifyPassword bounded by the argon2 semaphore; it
// returns ErrBusy if no slot frees up in time.
func VerifyPasswordCtx(ctx context.Context, encoded, pw string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errors.New("formato de hash inválido")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("versão de hash inválida")
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, errors.New("parâmetros de hash inválidos")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("salt inválido")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, errors.New("hash inválido")
	}
	var got []byte
	if err := withArgonSlot(ctx, func() {
		got = argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	}); err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
