package auth

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecoveryCodes(t *testing.T) {
	plain, hashes, err := GenerateRecoveryCodes()
	require.NoError(t, err)
	require.Len(t, plain, 10)
	require.Len(t, hashes, 10)

	re := regexp.MustCompile(`^[a-z2-9]{5}-[a-z2-9]{5}$`)
	seen := map[string]bool{}
	for i, c := range plain {
		require.Regexp(t, re, c)
		require.NotContains(t, c, "o")
		require.NotContains(t, c, "l")
		require.NotContains(t, c, "i")
		require.False(t, seen[c])
		seen[c] = true
		require.Equal(t, hashes[i], HashRecoveryCode(c))
	}
	require.Equal(t, HashRecoveryCode("abcde-fghij"), HashRecoveryCode(" ABCDE-fghij "))
	require.Equal(t, HashRecoveryCode("abcde-fghij"), HashRecoveryCode("abcde - fg hij"))
}
