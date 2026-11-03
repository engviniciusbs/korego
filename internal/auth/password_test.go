package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHashVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$"))

	ok, err := VerifyPassword(h, "correct horse battery")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = VerifyPassword(h, "wrong password!!")
	require.NoError(t, err)
	require.False(t, ok)

	_, err = VerifyPassword("lixo", "x")
	require.Error(t, err)
}

func TestPolicy(t *testing.T) {
	require.ErrorIs(t, ValidatePasswordPolicy("curta"), ErrWeakPassword)
	require.NoError(t, ValidatePasswordPolicy("123456789012"))
}
