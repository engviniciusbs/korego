package auth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestVerifyCode_TOTPRecoveryAndReplay(t *testing.T) {
	s, now := newService(t)
	ctx := context.Background()
	secret, codes := setupAdmin(t, s, *now)
	u, err := s.DB.UserByEmail(ctx, testEmail)
	require.NoError(t, err)

	ok, err := s.VerifyCode(ctx, u.ID, "")
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = s.VerifyCode(ctx, u.ID, "000000")
	require.NoError(t, err)
	require.False(t, ok)

	*now = now.Add(30 * time.Second)
	code := codeAt(t, secret, curStep(*now))
	ok, err = s.VerifyCode(ctx, u.ID, code)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = s.VerifyCode(ctx, u.ID, code) // anti-replay
	require.NoError(t, err)
	require.False(t, ok)

	ok, err = s.VerifyCode(ctx, u.ID, codes[0])
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = s.VerifyCode(ctx, u.ID, codes[0]) // uso único
	require.NoError(t, err)
	require.False(t, ok)

	_, err = s.VerifyCode(ctx, u.ID+99, code)
	require.Error(t, err)
}
