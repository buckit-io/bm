package nodes

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

func TestPutGetList(t *testing.T) {
	r := newFixture(t)
	ctx := context.Background()
	n := domain.Node{ID: "n1", ClusterID: "c1", Hostname: "host1", SSHPort: 22, State: domain.NodeOnline, Pool: 1}
	if err := r.Put(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := r.Put(ctx, domain.Node{ID: "n2", ClusterID: "c1", Hostname: "host2", SSHPort: 22, State: domain.NodeOnline, Pool: 1}); err != nil {
		t.Fatal(err)
	}
	if err := r.Put(ctx, domain.Node{ID: "n3", ClusterID: "c2", Hostname: "host3", SSHPort: 22, State: domain.NodeOnline, Pool: 1}); err != nil {
		t.Fatal(err)
	}

	got, err := r.List(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 nodes in c1, got %d", len(got))
	}
	one, err := r.Get(ctx, "c1", "n1")
	if err != nil {
		t.Fatal(err)
	}
	if one.Hostname != "host1" {
		t.Fatalf("bad fetched node: %+v", one)
	}
}

func TestGetMissing(t *testing.T) {
	r := newFixture(t)
	_, err := r.Get(context.Background(), "c1", "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	r := newFixture(t)
	ctx := context.Background()
	_ = r.Put(ctx, domain.Node{ID: "n1", ClusterID: "c", Hostname: "h", SSHPort: 22, Pool: 1})
	if err := r.Delete(ctx, "c", "n1"); err != nil {
		t.Fatal(err)
	}
	_, err := r.Get(ctx, "c", "n1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound after delete, got %v", err)
	}
}
