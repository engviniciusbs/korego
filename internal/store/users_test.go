package store

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func newAdmin() User {
	return User{Email: "  Admin@Exemplo.com ", PasswordHash: "h", TOTPSecretEnc: "enc"}
}

func TestCreateAdminIfNone_Once(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()

	id, err := db.CreateAdminIfNone(ctx, newAdmin(), []string{"a", "b"})
	require.NoError(t, err)
	require.NotZero(t, id)

	_, err = db.CreateAdminIfNone(ctx, newAdmin(), nil)
	require.ErrorIs(t, err, ErrSetupDone)

	n, err := db.CountUsers(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	u, err := db.UserByEmail(ctx, "ADMIN@exemplo.com")
	require.NoError(t, err)
	require.Equal(t, id, u.ID)
	require.Equal(t, "admin@exemplo.com", u.Email)
	require.Equal(t, "admin", u.Role)
	require.False(t, u.CreatedAt.IsZero())

	u2, err := db.UserByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, u.Email, u2.Email)

	_, err = db.UserByEmail(ctx, "nobody@exemplo.com")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = db.UserByID(ctx, 999)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestCreateAdminIfNone_Concurrent(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = db.CreateAdminIfNone(ctx, newAdmin(), []string{"x"})
		}()
	}
	wg.Wait()

	success, done := 0, 0
	for _, e := range errs {
		switch {
		case e == nil:
			success++
		case e == ErrSetupDone:
			done++
		default:
			t.Fatalf("erro inesperado: %v", e)
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, done)
	n, err := db.CountUsers(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestAdvanceTOTPStep(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	id, err := db.CreateAdminIfNone(ctx, newAdmin(), nil)
	require.NoError(t, err)

	ok, err := db.AdvanceTOTPStep(ctx, id, 5)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = db.AdvanceTOTPStep(ctx, id, 5)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = db.AdvanceTOTPStep(ctx, id, 4)
	require.NoError(t, err)
	require.False(t, ok)

	u, err := db.UserByID(ctx, id)
	require.NoError(t, err)
	require.EqualValues(t, 5, u.TOTPLastStep)
}

func TestUseRecoveryCode_SingleUse(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	id, err := db.CreateAdminIfNone(ctx, newAdmin(), []string{"h1", "h2"})
	require.NoError(t, err)

	ok, err := db.UseRecoveryCode(ctx, id, "h1")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = db.UseRecoveryCode(ctx, id, "h1")
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = db.UseRecoveryCode(ctx, id, "nope")
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = db.UseRecoveryCode(ctx, id, "h2")
	require.NoError(t, err)
	require.True(t, ok)
}
