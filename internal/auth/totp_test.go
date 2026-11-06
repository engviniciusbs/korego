package auth

import (
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

func codeAt(t *testing.T, secret string, step int64) string {
	t.Helper()
	c, err := totp.GenerateCodeCustom(secret, time.Unix(step*30, 0),
		totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	require.NoError(t, err)
	return c
}

func TestNewTOTPSecret(t *testing.T) {
	secret, url, err := NewTOTPSecret("admin@exemplo.com")
	require.NoError(t, err)
	require.NotEmpty(t, secret)
	require.Contains(t, url, "otpauth://totp/")
	require.Contains(t, url, "issuer=KoreGO")
}

func TestCheckTOTP_ValidAndSkew(t *testing.T) {
	secret, _, err := NewTOTPSecret("admin@exemplo.com")
	require.NoError(t, err)
	now := time.Unix(1_800_000_015, 0)
	cur := now.Unix() / 30

	for _, step := range []int64{cur - 1, cur, cur + 1} {
		got, ok := CheckTOTP(secret, codeAt(t, secret, step), now, 0)
		require.True(t, ok, "step %d", step)
		require.Equal(t, step, got)
	}
	_, ok := CheckTOTP(secret, codeAt(t, secret, cur-2), now, 0)
	require.False(t, ok)
	_, ok = CheckTOTP(secret, "000000x", now, 0)
	require.False(t, ok)
}

func TestCheckTOTP_Replay(t *testing.T) {
	secret, _, err := NewTOTPSecret("admin@exemplo.com")
	require.NoError(t, err)
	now := time.Unix(1_800_000_015, 0)
	cur := now.Unix() / 30
	_, ok := CheckTOTP(secret, codeAt(t, secret, cur), now, cur)
	require.False(t, ok)
	_, ok = CheckTOTP(secret, codeAt(t, secret, cur), now, cur-1)
	require.True(t, ok)
}
