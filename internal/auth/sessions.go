package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/engviniciusbs/korego/internal/oauth"
	"github.com/engviniciusbs/korego/internal/store"
)

// SessionTTL is the lifetime of a session.
const SessionTTL = 7 * 24 * time.Hour

const touchInterval = time.Minute

// ErrNoSession is returned when a token matches no live session.
var ErrNoSession = errors.New("sessão inexistente ou expirada")

// Sessions manages login sessions backed by the store.
type Sessions struct {
	DB  *store.DB
	Now func() time.Time
}

func (s *Sessions) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// hashToken é o SHA-256 hex do segredo; a mesma função usada pelo pacote oauth.
func hashToken(token string) string { return oauth.Hash(token) }

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("gerar aleatório: %w", err)
	}
	return b, nil
}

// Create starts a session and returns the plaintext token (never stored).
func (s *Sessions) Create(ctx context.Context, userID int64, ip, ua string) (string, store.Session, error) {
	tok, err := randomBytes(32)
	if err != nil {
		return "", store.Session{}, err
	}
	csrf, err := randomBytes(32)
	if err != nil {
		return "", store.Session{}, err
	}
	id, err := randomBytes(16)
	if err != nil {
		return "", store.Session{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(tok)
	now := s.now()
	sess := store.Session{
		ID:         hex.EncodeToString(id),
		TokenHash:  hashToken(token),
		UserID:     userID,
		CSRFToken:  base64.RawURLEncoding.EncodeToString(csrf),
		IP:         ip,
		UserAgent:  ua,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(SessionTTL),
	}
	if err := s.DB.CreateSession(ctx, sess); err != nil {
		return "", store.Session{}, err
	}
	return token, sess, nil
}

// Validate resolves a token to a live session, refreshing last_seen_at at most
// once per minute.
func (s *Sessions) Validate(ctx context.Context, token string) (store.Session, error) {
	sess, err := s.DB.SessionByTokenHash(ctx, hashToken(token))
	if errors.Is(err, store.ErrNotFound) {
		return store.Session{}, ErrNoSession
	}
	if err != nil {
		return store.Session{}, err
	}
	now := s.now()
	if !now.Before(sess.ExpiresAt) {
		return store.Session{}, ErrNoSession
	}
	if now.Sub(sess.LastSeenAt) >= touchInterval {
		if err := s.DB.TouchSession(ctx, sess.ID, now); err != nil {
			return store.Session{}, err
		}
		sess.LastSeenAt = now
	}
	return sess, nil
}
