package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func newTokenUser(t *testing.T, db *DB) int64 {
	t.Helper()
	id, err := db.CreateAdminIfNone(context.Background(), newAdmin(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func sampleToken(uid int64, name, hash string) APIToken {
	return APIToken{UserID: uid, Name: name, TokenHash: hash, Prefix: "kg_abcdefg",
		CreatedAt: time.Unix(1700000000, 0)}
}

func TestAPIToken_CreateAndLookup(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	uid := newTokenUser(t, db)

	in := sampleToken(uid, "ci", "h1")
	id, err := db.CreateAPIToken(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.APITokenByHash(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	in.ID, in.Kind = id, "manual"
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("got %+v, want %+v", got, in)
	}
	if got.LastUsedAt != nil || got.ExpiresAt != nil || got.RevokedAt != nil {
		t.Fatalf("campos opcionais deveriam ser nil: %+v", got)
	}

	exp := time.Unix(1800000000, 0)
	in2 := sampleToken(uid, "outro", "h2")
	in2.ExpiresAt = &exp
	if _, err := db.CreateAPIToken(ctx, in2); err != nil {
		t.Fatal(err)
	}
	got2, _ := db.APITokenByHash(ctx, "h2")
	if got2.ExpiresAt == nil || !got2.ExpiresAt.Equal(exp) {
		t.Fatalf("ExpiresAt = %v, want %v", got2.ExpiresAt, exp)
	}

	if _, err := db.APITokenByHash(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAPIToken_DuplicateActiveName(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	uid := newTokenUser(t, db)

	id, err := db.CreateAPIToken(ctx, sampleToken(uid, "ci", "h1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAPIToken(ctx, sampleToken(uid, "ci", "h2")); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if _, err := db.RevokeAPIToken(ctx, uid, id, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateAPIToken(ctx, sampleToken(uid, "ci", "h3")); err != nil {
		t.Fatalf("nome deveria poder ser reutilizado após revogar: %v", err)
	}
}

func TestAPIToken_Revoke(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	uid := newTokenUser(t, db)

	id, err := db.CreateAPIToken(ctx, sampleToken(uid, "ci", "h1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.RevokeAPIToken(ctx, uid+1, id, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outro usuário: err = %v, want ErrNotFound", err)
	}
	at := time.Unix(1700001000, 0)
	rev, err := db.RevokeAPIToken(ctx, uid, id, at)
	if err != nil {
		t.Fatal(err)
	}
	if rev.RevokedAt == nil || !rev.RevokedAt.Equal(at) || rev.ID != id {
		t.Fatalf("revogado = %+v", rev)
	}
	got, _ := db.APITokenByHash(ctx, "h1")
	if got.RevokedAt == nil || !got.RevokedAt.Equal(at) {
		t.Fatalf("RevokedAt = %v", got.RevokedAt)
	}
	list, err := db.ListAPITokens(ctx, uid)
	if err != nil || len(list) != 0 {
		t.Fatalf("lista = %v, %v; want vazia", list, err)
	}
	if _, err := db.RevokeAPIToken(ctx, uid, id, at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("segunda revogação: err = %v, want ErrNotFound", err)
	}
}

func TestAPIToken_ListNewestFirst(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	uid := newTokenUser(t, db)

	old := sampleToken(uid, "velho", "h1")
	nw := sampleToken(uid, "novo", "h2")
	nw.CreatedAt = old.CreatedAt.Add(time.Hour)
	for _, tk := range []APIToken{old, nw} {
		if _, err := db.CreateAPIToken(ctx, tk); err != nil {
			t.Fatal(err)
		}
	}
	list, err := db.ListAPITokens(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "novo" || list[1].Name != "velho" {
		t.Fatalf("ordem = %+v", list)
	}
}

func TestAPIToken_Touch(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	uid := newTokenUser(t, db)

	id, err := db.CreateAPIToken(ctx, sampleToken(uid, "ci", "h1"))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1700002000, 0)
	if err := db.TouchAPIToken(ctx, id, at, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	got, _ := db.APITokenByHash(ctx, "h1")
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(at) || got.LastUsedIP != "203.0.113.7" {
		t.Fatalf("got %+v", got)
	}
}

func TestAPIToken_UserCascade(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	uid := newTokenUser(t, db)

	if _, err := db.CreateAPIToken(ctx, sampleToken(uid, "ci", "h1")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.APITokenByHash(ctx, "h1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func oauthToken(uid int64) APIToken {
	t := sampleToken(uid, "Claude Code", "acc1")
	t.Kind, t.ClientID, t.RefreshHash = "oauth", "c1", "ref1"
	exp := time.Unix(1700003600, 0)
	t.AccessExpiresAt = &exp
	return t
}

func TestAPIToken_DefaultKindManual(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	uid := newTokenUser(t, db)
	if _, err := db.CreateAPIToken(ctx, sampleToken(uid, "ci", "h1")); err != nil {
		t.Fatal(err)
	}
	got, err := db.APITokenByHash(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "manual" || got.ClientID != "" || got.RefreshHash != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestAPIToken_RotateCAS(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	uid := newTokenUser(t, db)
	id, err := db.CreateAPIToken(ctx, oauthToken(uid))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1700001000, 0)
	rot := TokenRotation{AccessHash: "acc2", RefreshHash: "ref2", Prefix: "kg_newpref",
		AccessExpiresAt: at.Add(time.Hour), At: at}
	if err := db.RotateAPIToken(ctx, id, "ref1", rot); err != nil {
		t.Fatal(err)
	}
	got, err := db.APITokenByHash(ctx, "acc2")
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshHash != "ref2" || got.PrevRefreshHash != "ref1" || got.PrevAccessHash != "acc1" ||
		got.Prefix != "kg_newpref" || got.RotatedAt == nil || !got.RotatedAt.Equal(at) ||
		got.AccessExpiresAt == nil || !got.AccessExpiresAt.Equal(at.Add(time.Hour)) {
		t.Fatalf("got %+v", got)
	}
	if err := db.RotateAPIToken(ctx, id, "ref1", rot); !errors.Is(err, ErrConflict) {
		t.Fatalf("segunda rotação: err = %v, want ErrConflict", err)
	}
	byRef, err := db.APITokenByRefreshHash(ctx, "ref1")
	if err != nil || byRef.ID != id || byRef.PrevRefreshHash != "ref1" {
		t.Fatalf("por refresh antigo: %+v, %v", byRef, err)
	}
	if byRef, err = db.APITokenByRefreshHash(ctx, "ref2"); err != nil || byRef.ID != id {
		t.Fatalf("por refresh atual: %+v, %v", byRef, err)
	}
	byAcc, err := db.APITokenByPrevAccessHash(ctx, "acc1")
	if err != nil || byAcc.ID != id {
		t.Fatalf("por acesso antigo: %+v, %v", byAcc, err)
	}
	if _, err := db.APITokenByRefreshHash(ctx, "zzz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := db.APITokenByPrevAccessHash(ctx, "zzz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAPIToken_RotateKeepPrev(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	uid := newTokenUser(t, db)
	id, _ := db.CreateAPIToken(ctx, oauthToken(uid))
	t1 := time.Unix(1700001000, 0)
	if err := db.RotateAPIToken(ctx, id, "ref1", TokenRotation{AccessHash: "acc2", RefreshHash: "ref2",
		Prefix: "kg_p2xxxxx", AccessExpiresAt: t1.Add(time.Hour), At: t1}); err != nil {
		t.Fatal(err)
	}
	t2 := t1.Add(10 * time.Second)
	if err := db.RotateAPIToken(ctx, id, "ref2", TokenRotation{AccessHash: "acc3", RefreshHash: "ref3",
		Prefix: "kg_p3xxxxx", AccessExpiresAt: t2.Add(time.Hour), At: t2, KeepPrev: true}); err != nil {
		t.Fatal(err)
	}
	got, err := db.APITokenByHash(ctx, "acc3")
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshHash != "ref3" || got.PrevRefreshHash != "ref1" || got.PrevAccessHash != "acc1" ||
		got.RotatedAt == nil || !got.RotatedAt.Equal(t1) {
		t.Fatalf("got %+v", got)
	}
}

func TestAPIToken_RevokeByID(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	uid := newTokenUser(t, db)
	id, _ := db.CreateAPIToken(ctx, oauthToken(uid))
	at := time.Unix(1700002000, 0)
	if err := db.RevokeAPITokenByID(ctx, id, at); err != nil {
		t.Fatal(err)
	}
	got, _ := db.APITokenByHash(ctx, "acc1")
	if got.RevokedAt == nil || !got.RevokedAt.Equal(at) {
		t.Fatalf("RevokedAt = %v", got.RevokedAt)
	}
}
