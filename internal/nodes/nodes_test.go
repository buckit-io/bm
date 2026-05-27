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

func TestListSortedByHostnameNumeric(t *testing.T) {
	r := newFixture(t)
	ctx := context.Background()
	// Insert in shuffled order to prove sort happens on read, not insertion.
	for _, n := range []domain.Node{
		{ID: "n3", ClusterID: "c", Hostname: "buckit10", SSHPort: 22},
		{ID: "n1", ClusterID: "c", Hostname: "buckit2", SSHPort: 22},
		{ID: "n4", ClusterID: "c", Hostname: "buckit1", SSHPort: 22},
		{ID: "n2", ClusterID: "c", Hostname: "buckit3", SSHPort: 22},
	} {
		if err := r.Put(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.List(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"buckit1", "buckit2", "buckit3", "buckit10"}
	if len(got) != len(want) {
		t.Fatalf("want %d nodes, got %d", len(want), len(got))
	}
	for i, w := range want {
		if got[i].Hostname != w {
			t.Errorf("position %d: want %s, got %s", i, w, got[i].Hostname)
		}
	}
}

func TestCollateHostnames(t *testing.T) {
	cases := []struct {
		a, b string
		want int // negative => a<b, zero => equal, positive => a>b
	}{
		{"buckit1", "buckit2", -1},
		{"buckit2", "buckit10", -1},
		{"buckit10", "buckit2", 1},
		{"buckit1", "buckit1", 0},
		{"BUCKIT1", "buckit2", -1}, // case-insensitive on alpha runs
		{"host", "host1", -1},    // shorter alpha run sorts first
		{"a1b2", "a1b10", -1},    // multi-segment numeric-aware
	}
	for _, tc := range cases {
		got := collateHostnames(tc.a, tc.b)
		if (got < 0) != (tc.want < 0) || (got > 0) != (tc.want > 0) || (got == 0) != (tc.want == 0) {
			t.Errorf("collateHostnames(%q, %q) = %d, want sign of %d", tc.a, tc.b, got, tc.want)
		}
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
