package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/audit"
	"github.com/engviniciusbs/korego/internal/secrets"
	"github.com/engviniciusbs/korego/internal/store"
)

const (
	testEmail = "admin@exemplo.com"
	testPass  = "senha-forte-de-teste"
	testIP    = "203.0.113.10"
)

func newService(t *testing.T) (*Service, *time.Time) {
	t.Helper()
	db := store.OpenMemory(t)
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	box, err := secrets.NewBox(key)
	require.NoError(t, err)
	now := time.Unix(1_800_000_015, 0)
	clock := func() time.Time { return now }
	return &Service{
		DB:       db,
		Box:      box,
		Sessions: &Sessions{DB: db, Now: clock},
		Limiter:  NewLimiter(5, time.Minute, 15*time.Minute),
		Audit:    &audit.Logger{DB: db, Now: clock},
		Now:      clock,
	}, &now
}

func curStep(now time.Time) int64 { return now.Unix() / 30 }

// setupAdmin runs the full setup and returns the TOTP secret and recovery codes.
func setupAdmin(t *testing.T, s *Service, now time.Time) (string, []string) {
	t.Helper()
	ctx := context.Background()
	ch, err := s.BeginSetup(ctx, testEmail, testPass)
	require.NoError(t, err)
	res, codes, err := s.ConfirmSetup(ctx, ch.Token, codeAt(t, ch.Secret, curStep(now)), testIP, "ua")
	require.NoError(t, err)
	require.NotEmpty(t, res.Token)
	require.Len(t, codes, 10)
	return ch.Secret, codes
}

func TestBeginSetup_Validation(t *testing.T) {
	s, now := newService(t)
	ctx := context.Background()

	_, err := s.BeginSetup(ctx, testEmail, "curta")
	require.ErrorIs(t, err, ErrWeakPassword)
	for _, e := range []string{"", "nao-e-email", "Admin <admin@exemplo.com>", "a@b@c"} {
		_, err = s.BeginSetup(ctx, e, testPass)
		require.ErrorIs(t, err, ErrInvalidEmail, e)
	}

	setupAdmin(t, s, *now)
	_, err = s.BeginSetup(ctx, testEmail, testPass)
	require.ErrorIs(t, err, store.ErrSetupDone)
}

func TestSetup_ConcurrentConfirm_OneAdmin(t *testing.T) {
	s, now := newService(t)
	ctx := context.Background()

	var chs [2]SetupChallenge
	for i := range chs {
		ch, err := s.BeginSetup(ctx, testEmail, testPass)
		require.NoError(t, err)
		chs[i] = ch
	}
	require.NotEqual(t, chs[0].Token, chs[1].Token)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range chs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = s.ConfirmSetup(ctx, chs[i].Token, codeAt(t, chs[i].Secret, curStep(*now)), testIP, "ua")
		}()
	}
	wg.Wait()

	ok, done := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, store.ErrSetupDone):
			done++
		default:
			t.Fatalf("erro inesperado: %v", err)
		}
	}
	require.Equal(t, 1, ok)
	require.Equal(t, 1, done)
	n, err := s.DB.CountUsers(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestSetup_TokenInvalidCases(t *testing.T) {
	s, now := newService(t)
	ctx := context.Background()

	_, _, err := s.ConfirmSetup(ctx, "desconhecido", "123456", testIP, "ua")
	require.ErrorIs(t, err, ErrSetupTokenInvalid)

	ch, err := s.BeginSetup(ctx, testEmail, testPass)
	require.NoError(t, err)
	_, _, err = s.ConfirmSetup(ctx, ch.Token, "000000x", testIP, "ua")
	require.ErrorIs(t, err, ErrInvalidTOTP)

	*now = now.Add(16 * time.Minute)
	_, _, err = s.ConfirmSetup(ctx, ch.Token, codeAt(t, ch.Secret, curStep(*now)), testIP, "ua")
	require.ErrorIs(t, err, ErrSetupTokenInvalid)
}

func TestSetup_TokenSingleUse(t *testing.T) {
	s, now := newService(t)
	ctx := context.Background()
	ch, err := s.BeginSetup(ctx, testEmail, testPass)
	require.NoError(t, err)
	_, _, err = s.ConfirmSetup(ctx, ch.Token, codeAt(t, ch.Secret, curStep(*now)), testIP, "ua")
	require.NoError(t, err)

	*now = now.Add(30 * time.Second)
	_, _, err = s.ConfirmSetup(ctx, ch.Token, codeAt(t, ch.Secret, curStep(*now)), testIP, "ua")
	require.ErrorIs(t, err, ErrSetupTokenInvalid)
}

func TestSetup_StoresAdminRoleEncryptedSecretAndStep(t *testing.T) {
	s, now := newService(t)
	secret, _ := setupAdmin(t, s, *now)

	u, err := s.DB.UserByEmail(context.Background(), testEmail)
	require.NoError(t, err)
	require.Equal(t, "admin", u.Role)
	require.NotContains(t, u.TOTPSecretEnc, secret)
	require.Equal(t, curStep(*now), u.TOTPLastStep)

	// The code used to confirm the setup cannot be replayed at login.
	_, err = s.Login(context.Background(), testEmail, testPass, codeAt(t, secret, curStep(*now)), testIP, "ua")
	require.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestLogin_TOTPReplayRejected(t *testing.T) {
	s, now := newService(t)
	secret, _ := setupAdmin(t, s, *now)
	ctx := context.Background()

	*now = now.Add(30 * time.Second)
	c := codeAt(t, secret, curStep(*now))
	res, err := s.Login(ctx, " ADMIN@Exemplo.com ", testPass, c[:3]+" "+c[3:], testIP, "ua")
	require.NoError(t, err)
	require.NotEmpty(t, res.Token)
	require.NotEmpty(t, res.CSRFToken)
	require.Equal(t, testEmail, res.User.Email)

	_, err = s.Login(ctx, testEmail, testPass, c, testIP, "ua")
	require.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestLogin_RecoveryCodeSingleUse(t *testing.T) {
	s, now := newService(t)
	_, codes := setupAdmin(t, s, *now)
	ctx := context.Background()

	_, err := s.Login(ctx, testEmail, testPass, " "+codes[0]+" ", testIP, "ua")
	require.NoError(t, err)
	_, err = s.Login(ctx, testEmail, testPass, codes[0], testIP, "ua")
	require.ErrorIs(t, err, ErrInvalidCredentials)
	_, err = s.Login(ctx, testEmail, testPass, codes[1], testIP, "ua")
	require.NoError(t, err)
}

func TestLogin_RateLimitedBeforePasswordCheck(t *testing.T) {
	s, now := newService(t)
	_, codes := setupAdmin(t, s, *now)
	ctx := context.Background()

	for range 5 {
		_, err := s.Login(ctx, testEmail, "senha-errada-123", "000000", testIP, "ua")
		require.ErrorIs(t, err, ErrInvalidCredentials)
	}
	_, err := s.Login(ctx, testEmail, testPass, codes[0], testIP, "ua")
	require.ErrorIs(t, err, ErrRateLimited)

	// The recovery code was not consumed while blocked.
	*now = now.Add(16 * time.Minute)
	_, err = s.Login(ctx, testEmail, testPass, codes[0], testIP, "ua")
	require.NoError(t, err)
}

func TestLogout(t *testing.T) {
	s, now := newService(t)
	_, codes := setupAdmin(t, s, *now)
	ctx := context.Background()
	res, err := s.Login(ctx, testEmail, testPass, codes[0], testIP, "ua")
	require.NoError(t, err)

	sess, err := s.Sessions.Validate(ctx, res.Token)
	require.NoError(t, err)
	require.NoError(t, s.Logout(ctx, sess.ID))
	_, err = s.Sessions.Validate(ctx, res.Token)
	require.ErrorIs(t, err, ErrNoSession)
}
