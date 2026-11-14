package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/engviniciusbs/korego/internal/store"
)

// ErrNoToken is returned when a secret matches no usable API token.
var ErrNoToken = errors.New("token inválido")

// ErrInvalidTokenName is returned for empty or too long token names.
var ErrInvalidTokenName = errors.New("nome de token inválido")

const (
	tokenPrefix     = "kg_"
	tokenNameMaxLen = 64
	tokenPrefixLen  = 10
)

var tokenRe = regexp.MustCompile(`^kg_[A-Za-z0-9_-]{43}$`)

// Tokens manages API tokens backed by the store.
type Tokens struct {
	DB  *store.DB
	Now func() time.Time
}

func (t *Tokens) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// Issue creates a token and returns its plaintext secret (never stored).
// ttl 0 means the token never expires.
func (t *Tokens) Issue(ctx context.Context, userID int64, name string, ttl time.Duration) (string, store.APIToken, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > tokenNameMaxLen {
		return "", store.APIToken{}, ErrInvalidTokenName
	}
	raw, err := randomBytes(32)
	if err != nil {
		return "", store.APIToken{}, err
	}
	secret := tokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	now := t.now()
	tok := store.APIToken{
		UserID:    userID,
		Name:      name,
		TokenHash: hashToken(secret),
		Prefix:    secret[:tokenPrefixLen],
		CreatedAt: now,
	}
	if ttl > 0 {
		exp := now.Add(ttl)
		tok.ExpiresAt = &exp
	}
	id, err := t.DB.CreateAPIToken(ctx, tok)
	if err != nil {
		return "", store.APIToken{}, err
	}
	tok.ID = id
	return secret, tok, nil
}

// Validate resolves a secret to a live token and records its use (at most
// once per minute). Malformed, unknown, revoked and expired secrets give ErrNoToken.
func (t *Tokens) Validate(ctx context.Context, secret, ip string) (store.APIToken, error) {
	if !tokenRe.MatchString(secret) {
		return store.APIToken{}, ErrNoToken
	}
	tok, err := t.DB.APITokenByHash(ctx, hashToken(secret))
	if errors.Is(err, store.ErrNotFound) {
		return store.APIToken{}, ErrNoToken
	}
	if err != nil {
		return store.APIToken{}, err
	}
	now := t.now()
	if tok.RevokedAt != nil || (tok.ExpiresAt != nil && !now.Before(*tok.ExpiresAt)) {
		return store.APIToken{}, ErrNoToken
	}
	if tok.LastUsedAt == nil || now.Sub(*tok.LastUsedAt) >= touchInterval {
		if err := t.DB.TouchAPIToken(ctx, tok.ID, now, ip); err != nil {
			return store.APIToken{}, err
		}
		tok.LastUsedAt, tok.LastUsedIP = &now, ip
	}
	return tok, nil
}
