package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestOAuthClient_CreateGetAuthorize(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	in := OAuthClient{ID: "c1", Name: "Claude Code",
		RedirectURIs: []string{"http://127.0.0.1:1234/cb", "http://localhost/cb"},
		CreatedAt:    time.Unix(1700000000, 0)}
	if err := db.CreateOAuthClient(ctx, in); err != nil {
		t.Fatal(err)
	}
	got, err := db.OAuthClientByID(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("got %+v, want %+v", got, in)
	}
	if got.AuthorizedAt != nil {
		t.Fatal("AuthorizedAt deveria ser nil")
	}
	at := time.Unix(1700000100, 0)
	if err := db.MarkOAuthClientAuthorized(ctx, "c1", at); err != nil {
		t.Fatal(err)
	}
	got, _ = db.OAuthClientByID(ctx, "c1")
	if got.AuthorizedAt == nil || !got.AuthorizedAt.Equal(at) {
		t.Fatalf("AuthorizedAt = %v, want %v", got.AuthorizedAt, at)
	}
	if _, err := db.OAuthClientByID(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if err := db.CreateOAuthClient(ctx, in); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestOAuthClient_PruneAndEvict(t *testing.T) {
	db := OpenMemory(t)
	ctx := context.Background()
	mk := func(id string, created int64) {
		t.Helper()
		if err := db.CreateOAuthClient(ctx, OAuthClient{ID: id, Name: id,
			RedirectURIs: []string{"http://localhost/cb"}, CreatedAt: time.Unix(created, 0)}); err != nil {
			t.Fatal(err)
		}
	}
	mk("u2", 200)
	mk("u1", 100)
	mk("u3", 300)
	mk("auth", 50)
	if err := db.MarkOAuthClientAuthorized(ctx, "auth", time.Unix(400, 0)); err != nil {
		t.Fatal(err)
	}
	if n, err := db.CountUnauthorizedOAuthClients(ctx); err != nil || n != 3 {
		t.Fatalf("count = %d, %v; want 3", n, err)
	}
	if err := db.DeleteOldestUnauthorizedOAuthClient(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.OAuthClientByID(ctx, "u1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("u1 deveria ter saído: %v", err)
	}
	n, err := db.PruneOAuthClients(ctx, time.Unix(1000, 0))
	if err != nil || n != 2 {
		t.Fatalf("prune = %d, %v; want 2", n, err)
	}
	if _, err := db.OAuthClientByID(ctx, "auth"); err != nil {
		t.Fatalf("autorizado não pode ser apagado: %v", err)
	}
}
