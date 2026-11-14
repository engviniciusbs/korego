package auth

import (
	"context"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/oauth"
	"github.com/engviniciusbs/korego/internal/store"
)

func newTokens(t *testing.T) (*Tokens, int64, *time.Time) {
	t.Helper()
	db := store.OpenMemory(t)
	uid, err := db.CreateAdminIfNone(context.Background(),
		store.User{Email: "admin@exemplo.com", PasswordHash: "h", TOTPSecretEnc: "enc"}, nil)
	require.NoError(t, err)
	now := time.Unix(1_800_000_000, 0)
	return &Tokens{DB: db, Key: []byte("chave-de-teste-0123456789abcdef!"), Now: func() time.Time { return now }}, uid, &now
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
	require.ErrorIs(t, err, ErrStaleToken)

	exp, _, err := tk.Issue(ctx, uid, "expira", time.Hour)
	require.NoError(t, err)
	_, err = tk.Validate(ctx, exp, "1.1.1.1")
	require.NoError(t, err)
	*now = now.Add(time.Hour)
	_, err = tk.Validate(ctx, exp, "1.1.1.1")
	require.ErrorIs(t, err, ErrStaleToken)
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

func TestIssueOAuth_UniqueName(t *testing.T) {
	tk, uid, _ := newTokens(t)
	ctx := context.Background()
	var names []string
	for i := 0; i < 3; i++ {
		pair, tok, err := tk.IssueOAuth(ctx, uid, "Claude Code", "cid", 90*24*time.Hour)
		require.NoError(t, err)
		names = append(names, tok.Name)
		require.Regexp(t, `^kgr_[A-Za-z0-9_-]{43}$`, pair.Refresh)
		require.Regexp(t, `^kg_[A-Za-z0-9_-]{43}$`, pair.Access)
		require.Equal(t, time.Hour, pair.ExpiresIn)
		require.Equal(t, "oauth", tok.Kind)
	}
	require.Equal(t, []string{"Claude Code", "Claude Code (2)", "Claude Code (3)"}, names)

	// Nome longo é cortado para caber com o sufixo em 64 runas.
	long := repeat("é", 70)
	_, a, err := tk.IssueOAuth(ctx, uid, long, "cid", 0)
	require.NoError(t, err)
	_, b, err := tk.IssueOAuth(ctx, uid, long, "cid", 0)
	require.NoError(t, err)
	require.Equal(t, 64, len([]rune(a.Name)))
	require.Equal(t, 64, len([]rune(b.Name)))
	require.Contains(t, b.Name, " (2)")
}

func TestValidate_AccessExpiresStale(t *testing.T) {
	tk, uid, now := newTokens(t)
	ctx := context.Background()
	pair, _, err := tk.IssueOAuth(ctx, uid, "Cursor", "cid", 90*24*time.Hour)
	require.NoError(t, err)
	start := *now
	*now = start.Add(59 * time.Minute)
	_, err = tk.Validate(ctx, pair.Access, "1.1.1.1")
	require.NoError(t, err)
	*now = start.Add(61 * time.Minute)
	_, err = tk.Validate(ctx, pair.Access, "1.1.1.1")
	require.ErrorIs(t, err, ErrStaleToken)
}

func TestValidate_UnknownVsStale(t *testing.T) {
	tk, uid, now := newTokens(t)
	ctx := context.Background()
	rnd, err := oauth.RandomToken()
	require.NoError(t, err)
	_, err = tk.Validate(ctx, "kg_"+rnd, "1.1.1.1")
	require.ErrorIs(t, err, ErrNoToken)

	secret, tok, err := tk.Issue(ctx, uid, "rev", 0)
	require.NoError(t, err)
	require.NoError(t, tk.RevokeByID(ctx, tok.ID))
	_, err = tk.Validate(ctx, secret, "1.1.1.1")
	require.ErrorIs(t, err, ErrStaleToken)

	pair, _, err := tk.IssueOAuth(ctx, uid, "VS Code", "cid", 0)
	require.NoError(t, err)
	*now = now.Add(time.Minute)
	_, _, err = tk.Refresh(ctx, pair.Refresh, "cid")
	require.NoError(t, err)
	_, err = tk.Validate(ctx, pair.Access, "1.1.1.1")
	require.ErrorIs(t, err, ErrStaleToken)
}

func TestRefresh_RotatesAndGrace(t *testing.T) {
	tk, uid, now := newTokens(t)
	ctx := context.Background()
	p1, _, err := tk.IssueOAuth(ctx, uid, "Codex", "cid", 0)
	require.NoError(t, err)
	*now = now.Add(time.Minute)
	t0 := *now
	p2, _, err := tk.Refresh(ctx, p1.Refresh, "cid")
	require.NoError(t, err)
	require.NotEqual(t, p1.Refresh, p2.Refresh)
	_, err = tk.Validate(ctx, p1.Access, "1.1.1.1")
	require.ErrorIs(t, err, ErrStaleToken)

	*now = t0.Add(30 * time.Second)
	again, _, err := tk.Refresh(ctx, p1.Refresh, "cid")
	require.NoError(t, err)
	require.Equal(t, p2.Access, again.Access)
	require.Equal(t, p2.Refresh, again.Refresh)
	_, err = tk.Validate(ctx, p2.Access, "1.1.1.1")
	require.NoError(t, err)
	p3, _, err := tk.Refresh(ctx, p2.Refresh, "cid")
	require.NoError(t, err)
	require.NotEqual(t, p2.Refresh, p3.Refresh)

	// Depois de rotacionar de novo, R1 na janela não casa mais com o par atual.
	_, _, err = tk.Refresh(ctx, p1.Refresh, "cid")
	require.ErrorIs(t, err, ErrNoToken)

	// p2 virou o "anterior" de p3 (rotacionado em t0+30s); fora da janela é reuso.
	*now = t0.Add(100 * time.Second)
	_, _, err = tk.Refresh(ctx, p2.Refresh, "cid")
	require.ErrorIs(t, err, ErrRefreshReuse)
	_, err = tk.Validate(ctx, p3.Access, "1.1.1.1")
	require.ErrorIs(t, err, ErrStaleToken)
}

func TestRefresh_ReuseAfterWindow(t *testing.T) {
	tk, uid, now := newTokens(t)
	ctx := context.Background()
	p1, _, err := tk.IssueOAuth(ctx, uid, "Codex", "cid", 0)
	require.NoError(t, err)
	t0 := *now
	p2, _, err := tk.Refresh(ctx, p1.Refresh, "cid")
	require.NoError(t, err)
	*now = t0.Add(90 * time.Second)
	_, _, err = tk.Refresh(ctx, p1.Refresh, "cid")
	require.ErrorIs(t, err, ErrRefreshReuse)
	_, err = tk.Validate(ctx, p2.Access, "1.1.1.1")
	require.ErrorIs(t, err, ErrStaleToken)
}

func TestRefresh_EmptyKey(t *testing.T) {
	tk, uid, _ := newTokens(t)
	ctx := context.Background()
	p, _, err := tk.IssueOAuth(ctx, uid, "X", "cid", 0)
	require.NoError(t, err)
	tk.Key = nil
	_, _, err = tk.Refresh(ctx, p.Refresh, "cid")
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNoToken)
}

func TestDeriveKey(t *testing.T) {
	m := []byte("mestra")
	require.Equal(t, DeriveKey(m, "a"), DeriveKey(m, "a"))
	require.NotEqual(t, DeriveKey(m, "a"), DeriveKey(m, "b"))
	require.NotEqual(t, DeriveKey(m, "a"), DeriveKey([]byte("outra"), "a"))
	require.Len(t, DeriveKey(m, "a"), 32)
}

func TestRefresh_ClientMismatch(t *testing.T) {
	tk, uid, _ := newTokens(t)
	ctx := context.Background()
	p, _, err := tk.IssueOAuth(ctx, uid, "Gemini", "cid", 0)
	require.NoError(t, err)
	_, _, err = tk.Refresh(ctx, p.Refresh, "outro")
	require.ErrorIs(t, err, ErrNoToken)
	_, err = tk.Validate(ctx, p.Access, "1.1.1.1")
	require.NoError(t, err)
	_, _, err = tk.Refresh(ctx, "kgr_curto", "cid")
	require.ErrorIs(t, err, ErrNoToken)
}

func TestRefresh_ConcurrentSameRefresh(t *testing.T) {
	tk, uid, _ := newTokens(t)
	ctx := context.Background()
	p, _, err := tk.IssueOAuth(ctx, uid, "Windsurf", "cid", 0)
	require.NoError(t, err)

	start := make(chan struct{})
	pairs := make([]OAuthPair, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range pairs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			pairs[i], _, errs[i] = tk.Refresh(ctx, p.Refresh, "cid")
		}()
	}
	close(start)
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	require.Equal(t, pairs[0].Access, pairs[1].Access)
	require.Equal(t, pairs[0].Refresh, pairs[1].Refresh)
	_, err = tk.Validate(ctx, pairs[0].Access, "1.1.1.1")
	require.NoError(t, err)
	cur, err := tk.DB.APITokenByRefreshHash(ctx, hashToken(pairs[0].Refresh))
	require.NoError(t, err)
	require.Nil(t, cur.RevokedAt)
	_, _, err = tk.Refresh(ctx, pairs[0].Refresh, "cid")
	require.NoError(t, err)
}

func TestRefresh_CapsAtExpiresAt(t *testing.T) {
	tk, uid, now := newTokens(t)
	ctx := context.Background()
	p, _, err := tk.IssueOAuth(ctx, uid, "Claude Code", "cid", 30*time.Minute)
	require.NoError(t, err)
	start := *now
	*now = start.Add(10 * time.Minute)
	p2, _, err := tk.Refresh(ctx, p.Refresh, "cid")
	require.NoError(t, err)
	require.LessOrEqual(t, p2.ExpiresIn, 20*time.Minute)
	*now = start.Add(31 * time.Minute)
	_, _, err = tk.Refresh(ctx, p2.Refresh, "cid")
	require.ErrorIs(t, err, ErrNoToken)
}
