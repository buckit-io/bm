package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"path/filepath"
	"testing"

	"go.etcd.io/bbolt"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "bm.db"), key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenCreatesBuckets(t *testing.T) {
	s := newTestStore(t)
	if err := s.View(context.Background(), func(tx *bbolt.Tx) error {
		for _, want := range bucketsToCreate {
			if tx.Bucket(want) == nil {
				t.Errorf("bucket %s not created", want)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEncryptedRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	payload := make([]byte, 1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	if err := s.PutEncrypted(ctx, BucketClusterAdmin, []byte("c1"), payload); err != nil {
		t.Fatalf("PutEncrypted: %v", err)
	}
	got, err := s.GetEncrypted(ctx, BucketClusterAdmin, []byte("c1"))
	if err != nil {
		t.Fatalf("GetEncrypted: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}

func TestGetEncryptedMissingKey(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetEncrypted(context.Background(), BucketClusterAdmin, []byte("nope"))
	if err != nil {
		t.Fatalf("GetEncrypted on absent key: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil for absent key, got %d bytes", len(got))
	}
}

func TestSecondOpenIsBusy(t *testing.T) {
	dir := t.TempDir()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	path := filepath.Join(dir, "bm.db")
	s1, err := Open(path, key)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	defer s1.Close()
	if _, err := Open(path, key); err == nil {
		t.Fatal("expected second Open to fail while first holds the lock")
	}
}
