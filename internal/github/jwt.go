// Package github implementa o cliente do GitHub App (JWT, tokens de instalação, repositórios).
package github

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// ParsePrivateKey lê uma chave RSA em PEM (PKCS#1 do GitHub; PKCS#8 como alternativa).
func ParsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("github: chave privada não está em formato PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("github: chave privada inválida")
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("github: a chave privada não é RSA")
	}
	return rk, nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// AppJWT gera o JWT RS256 que autentica o App (iat=now-60s, exp=now+9min).
func AppJWT(appID int64, key *rsa.PrivateKey, now time.Time) (string, error) {
	claims, err := json.Marshal(map[string]any{
		"iat": now.Unix() - 60,
		"exp": now.Unix() + 540,
		"iss": strconv.FormatInt(appID, 10),
	})
	if err != nil {
		return "", err
	}
	signing := b64([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + b64(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("github: assinar JWT: %w", err)
	}
	return signing + "." + b64(sig), nil
}
