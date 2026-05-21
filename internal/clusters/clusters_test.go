package clusters

import (
	"context"
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"

	"go.etcd.io/bbolt"

	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/store"
)

func newFixture(t *testing.T) (*store.Store, *Repo) {
	t.Helper()
	dir := t.TempDir()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	s, err := store.Open(filepath.Join(dir, "bm.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, New(s)
}

func TestPutGetList(t *testing.T) {
	_, r := newFixture(t)
	ctx := context.Background()
	c := domain.Cluster{ID: "c1", Name: "prod", Engine: domain.EngineBuckit, Version: "v1"}
	if err := r.Put(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "prod" {
		t.Fatalf("name round-trip: %+v", got)
	}
	list, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 cluster, got %d", len(list))
	}
}

func TestGetMissing(t *testing.T) {
	_, r := newFixture(t)
	_, err := r.Get(context.Background(), "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDeleteCascades(t *testing.T) {
	s, r := newFixture(t)
	ctx := context.Background()
	_ = r.Put(ctx, domain.Cluster{ID: "c1", Name: "prod"})

	// Seed dependent buckets directly.
	mustPut := func(bucket, key, value []byte) {
		err := s.Update(ctx, func(tx *bbolt.Tx) error {
			b := tx.Bucket(bucket)
			return b.Put(key, value)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	mustPut(store.BucketNodes, []byte("c1:n1"), []byte("{}"))
	mustPut(store.BucketNodes, []byte("c1:n2"), []byte("{}"))
	mustPut(store.BucketNodes, []byte("c2:n1"), []byte("{}")) // unrelated
	mustPut(store.BucketNodeFacts, []byte("c1:n1"), []byte("{}"))
	mustPut(store.BucketClusterSSH, []byte("c1"), []byte("ciphertext"))
	mustPut(store.BucketClusterAdmin, []byte("c1"), []byte("ciphertext"))

	if err := r.Delete(ctx, "c1"); err != nil {
		t.Fatal(err)
	}

	check := func(bucket []byte, key string, wantNil bool) {
		err := s.View(ctx, func(tx *bbolt.Tx) error {
			b := tx.Bucket(bucket)
			v := b.Get([]byte(key))
			if wantNil && v != nil {
				t.Errorf("%s/%s should be nil, got %v", bucket, key, v)
			}
			if !wantNil && v == nil {
				t.Errorf("%s/%s should exist", bucket, key)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	check(store.BucketClusters, "c1", true)
	check(store.BucketNodes, "c1:n1", true)
	check(store.BucketNodes, "c1:n2", true)
	check(store.BucketNodes, "c2:n1", false) // unrelated cluster's node survives
	check(store.BucketNodeFacts, "c1:n1", true)
	check(store.BucketClusterSSH, "c1", true)
	check(store.BucketClusterAdmin, "c1", true)
}
