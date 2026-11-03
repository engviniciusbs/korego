package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

const (
	recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	recoveryCount    = 10
	recoveryHalf     = 5
)

// GenerateRecoveryCodes returns 10 unique plain codes and their hashes.
func GenerateRecoveryCodes() (plain []string, hashes []string, err error) {
	seen := map[string]bool{}
	for len(plain) < recoveryCount {
		c, err := newRecoveryCode()
		if err != nil {
			return nil, nil, err
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		plain = append(plain, c)
		hashes = append(hashes, HashRecoveryCode(c))
	}
	return plain, hashes, nil
}

func newRecoveryCode() (string, error) {
	// Rejection sampling keeps the distribution uniform over the alphabet.
	n := len(recoveryAlphabet)
	limit := 256 - 256%n
	out := make([]byte, 0, 2*recoveryHalf)
	buf := make([]byte, 16)
	for len(out) < 2*recoveryHalf {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("gerar código: %w", err)
		}
		for _, b := range buf {
			if int(b) >= limit || len(out) >= 2*recoveryHalf {
				continue
			}
			out = append(out, recoveryAlphabet[int(b)%n])
		}
	}
	return string(out[:recoveryHalf]) + "-" + string(out[recoveryHalf:]), nil
}

// HashRecoveryCode returns the SHA-256 hex of the normalized code
// (lowercase, all whitespace removed).
func HashRecoveryCode(code string) string {
	norm := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, code)
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])
}
