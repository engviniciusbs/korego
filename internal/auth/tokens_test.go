package auth

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/store"
)

func newTokens(t *testing.T) (*Tokens, int64, *time.Time) {
	t.Helper()
	db := store.OpenMemory(t)
	uid, err := db.CreateAdminIfNone(context.Background(),
		store.User{Email: "admin@exemplo.com", PasswordHash: "h", TOTPSecretEnc: "enc"}, nil)
	require.NoError(t, err)
	now := time.Unix(1_800_000_000, 0)
	return &Tokens{DB: db, Now: func() time.Time { return now }}, uid, &now
}

func TestTokens_IssueFormat(t *testing.T) {
	tk, uid, _ := newTokens(t)
	secret, tok, err := tk.Issue(context.Background(), uid, "notebook", 0)
	require.NoError(t, err)
	require.Regexp(t, regexp.MustCompile(`^kg_[A-Za-z0-9_-]{43}$`), secret)
	require.Equal(t, secret[:10], tok.Prefix)
	require.Equal(t, hashToken(secret), tok.TokenHash)
	require.Nil(t, tok.ExpiresAt)

	var n int
	require.NoError(t, tk.DB.SQL().QueryRow(
		`SELECT COUNT(*) FROM api_tokens WHERE token_hash = ? AND prefix = ?`,
		hashToken(secret), secret[:10]).Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, tk.DB.SQL().QueryRow(
		`SELECT COUNT(*) FROM api_tokens WHERE token_hash = ? OR name = ? OR prefix = ?`,
		secret, secret, secret).Scan(&n))
	require.Equal(t, 0, n)
}

func TestTokens_ValidateRejects(t *testing.T) {
	tk, uid, now := newTokens(t)
	ctx := context.Background()

	_, err := tk.Validate(ctx, "abc", "1.1.1.1")
	require.ErrorIs(t, err, ErrNoToken)
	_, err = tk.Validate(ctx, "kg_", "1.1.1.1")
	require.ErrorIs(t, err, ErrNoToken)

	other, _, err := tk.Issue(ctx, uid, "outro", 0)
	require.NoError(t, err)
	unknown := other[:len(other)-1] + map[bool]string{true: "B", false: "A"}[other[len(other)-1] == 'A']
	_, err = tk.Validate(ctx, unknown, "1.1.1.1")
	require.ErrorIs(t, err, ErrNoToken)

	secret, tok, err := tk.Issue(ctx, uid, "revogado", 0)
	require.NoError(t, err)
	_, err = tk.Validate(ctx, secret, "1.1.1.1")
	require.NoError(t, err)
	_, err = tk.DB.RevokeAPIToken(ctx, uid, tok.ID, *now)
	require.NoError(t, err)
	_, err = tk.Validate(ctx, secret, "1.1.1.1")
	require.ErrorIs(t, err, ErrNoToken)

	exp, _, err := tk.Issue(ctx, uid, "expira", time.Hour)
	require.NoError(t, err)
	_, err = tk.Validate(ctx, exp, "1.1.1.1")
	require.NoError(t, err)
	*now = now.Add(time.Hour)
	_, err = tk.Validate(ctx, exp, "1.1.1.1")
	require.ErrorIs(t, err, ErrNoToken)
}

func TestTokens_TouchThrottled(t *testing.T) {
	tk, uid, now := newTokens(t)
	ctx := context.Background()
	secret, tok, err := tk.Issue(ctx, uid, "nb", 0)
	require.NoError(t, err)

	start := *now
	_, err = tk.Validate(ctx, secret, "1.1.1.1")
	require.NoError(t, err)
	*now = start.Add(30 * time.Second)
	_, err = tk.Validate(ctx, secret, "2.2.2.2")
	require.NoError(t, err)

	got, err := tk.DB.APITokenByHash(ctx, tok.TokenHash)
	require.NoError(t, err)
	require.Equal(t, start.Unix(), got.LastUsedAt.Unix())
	require.Equal(t, "1.1.1.1", got.LastUsedIP)

	*now = start.Add(61 * time.Second)
	_, err = tk.Validate(ctx, secret, "3.3.3.3")
	require.NoError(t, err)
	got, err = tk.DB.APITokenByHash(ctx, tok.TokenHash)
	require.NoError(t, err)
	require.Equal(t, start.Add(61*time.Second).Unix(), got.LastUsedAt.Unix())
	require.Equal(t, "3.3.3.3", got.LastUsedIP)
}

func TestTokens_IssueNameRules(t *testing.T) {
	tk, uid, _ := newTokens(t)
	ctx := context.Background()
	for _, n := range []string{"  ", ""} {
		_, _, err := tk.Issue(ctx, uid, n, 0)
		require.ErrorIs(t, err, ErrInvalidTokenName)
	}
	_, _, err := tk.Issue(ctx, uid, repeat("a", 65), 0)
	require.ErrorIs(t, err, ErrInvalidTokenName)

	_, tok, err := tk.Issue(ctx, uid, "  meu nb  ", 0)
	require.NoError(t, err)
	require.Equal(t, "meu nb", tok.Name)
	_, _, err = tk.Issue(ctx, uid, "meu nb", 0)
	require.ErrorIs(t, err, store.ErrConflict)
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
