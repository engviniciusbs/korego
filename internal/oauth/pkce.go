package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strings"
)

// VerifyPKCE confere o code_verifier contra o code_challenge (método S256).
func VerifyPKCE(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	for i := 0; i < len(verifier); i++ {
		c := verifier[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_', c == '~':
		default:
			return false
		}
	}
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(want), []byte(challenge)) == 1
}

// NormalizeResource põe o resource (RFC 8707) na forma canônica: esquema e
// host em minúsculas, sem :443, sem barra final, sem query nem fragmento.
func NormalizeResource(s string) (string, bool) {
	if s == "" || strings.Contains(s, "#") {
		return "", false
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.Opaque != "" || u.User != nil {
		return "", false
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return "", false
	}
	host := strings.ToLower(u.Host)
	if scheme == "https" {
		host = strings.TrimSuffix(host, ":443")
	}
	return scheme + "://" + host + strings.TrimRight(u.Path, "/"), true
}

// RandomToken devolve 32 bytes aleatórios em base64url (43 caracteres).
func RandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Hash é o SHA-256 em hex, no mesmo formato de internal/auth (hashToken).
func Hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
