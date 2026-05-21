package clusteradmin

import (
	"context"
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"

	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/store"
)

func newFixture(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	s, err := store.Open(filepath.Join(dir, "bm.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return New(s)
}

func TestPutGetRoundTrip(t *testing.T) {
	r := newFixture(t)
	ctx := context.Background()
	in := domain.AdminCreds{URL: "https://prod-east:9000", AccessKey: "ak", SecretKey: "sk", Insecure: true}
	if err := r.Put(ctx, "c1", in); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got != in {
		t.Fatalf("round trip mismatch: got %+v", got)
	}
}

func TestGetMissing(t *testing.T) {
	r := newFixture(t)
	_, err := r.Get(context.Background(), "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestValidation(t *testing.T) {
	r := newFixture(t)
	cases := []domain.AdminCreds{
		{AccessKey: "ak", SecretKey: "sk"},
		{URL: "x", SecretKey: "sk"},
		{URL: "x", AccessKey: "ak"},
	}
	for i, c := range cases {
		if err := r.Put(context.Background(), "c", c); err == nil {
			t.Fatalf("case %d should have failed validation", i)
		}
	}
}
