package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/store"
)

func newSessions(t *testing.T) (*Sessions, int64, *time.Time) {
	t.Helper()
	db := store.OpenMemory(t)
	uid, err := db.CreateAdminIfNone(context.Background(),
		store.User{Email: "admin@exemplo.com", PasswordHash: "h", TOTPSecretEnc: "enc"}, nil)
	require.NoError(t, err)
	now := time.Unix(1_800_000_000, 0)
	return &Sessions{DB: db, Now: func() time.Time { return now }}, uid, &now
}

func TestSessions_CreateValidate(t *testing.T) {
	s, uid, _ := newSessions(t)
	ctx := context.Background()

	token, created, err := s.Create(ctx, uid, "203.0.113.10", "ua")
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.Len(t, created.ID, 32)
	require.NotEmpty(t, created.CSRFToken)

	got, err := s.Validate(ctx, token)
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, uid, got.UserID)
	require.Equal(t, created.CSRFToken, got.CSRFToken)

	_, err = s.Validate(ctx, token+"x")
	require.ErrorIs(t, err, ErrNoSession)
}

func TestSessions_StoresOnlyHash(t *testing.T) {
	s, uid, _ := newSessions(t)
	token, _, err := s.Create(context.Background(), uid, "203.0.113.10", "ua")
	require.NoError(t, err)

	var n int
	require.NoError(t, s.DB.SQL().QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE token_hash = ? OR id = ? OR csrf_token = ?`,
		token, token, token).Scan(&n))
	require.Zero(t, n)

	var hash string
	require.NoError(t, s.DB.SQL().QueryRow(`SELECT token_hash FROM sessions`).Scan(&hash))
	require.Len(t, hash, 64)
}

func TestSessions_Expired(t *testing.T) {
	s, uid, now := newSessions(t)
	token, _, err := s.Create(context.Background(), uid, "203.0.113.10", "ua")
	require.NoError(t, err)

	*now = now.Add(SessionTTL - time.Second)
	_, err = s.Validate(context.Background(), token)
	require.NoError(t, err)

	*now = now.Add(2 * time.Second)
	_, err = s.Validate(context.Background(), token)
	require.ErrorIs(t, err, ErrNoSession)
}

func TestSessions_TouchAtMostOncePerMinute(t *testing.T) {
	s, uid, now := newSessions(t)
	ctx := context.Background()
	token, created, err := s.Create(ctx, uid, "203.0.113.10", "ua")
	require.NoError(t, err)

	*now = now.Add(30 * time.Second)
	got, err := s.Validate(ctx, token)
	require.NoError(t, err)
	require.Equal(t, created.LastSeenAt.Unix(), got.LastSeenAt.Unix())

	*now = now.Add(31 * time.Second)
	_, err = s.Validate(ctx, token)
	require.NoError(t, err)
	list, err := s.DB.ListSessions(ctx, uid)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, now.Unix(), list[0].LastSeenAt.Unix())
}
