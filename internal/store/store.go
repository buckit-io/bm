// Package store wraps bbolt with the bucket layout described in
// docs/manager/backend-design.md and provides AES-GCM helpers for the buckets
// that hold credentials (cluster_ssh, cluster_admin).
package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"go.etcd.io/bbolt"
)

// Bucket names. Add new buckets here so first-open initialisation covers them.
var bucketsToCreate = [][]byte{
	BucketClusters,
	BucketNodes,
	BucketNodeFacts,
	BucketClusterSSH,
	BucketClusterAdmin,
	BucketHistory,
	BucketSettings,
}

// Exported bucket name constants used by callers.
var (
	BucketClusters     = []byte("clusters")
	BucketNodes        = []byte("nodes")
	BucketNodeFacts    = []byte("node_facts")
	BucketClusterSSH   = []byte("cluster_ssh")
	BucketClusterAdmin = []byte("cluster_admin")
	BucketHistory      = []byte("history")
	BucketSettings     = []byte("settings")
)

const (
	openTimeout = 5 * time.Second
	txnTimeout  = 5 * time.Second
	filePerm    = 0o600
)

// ErrBusy is returned when bbolt's file lock is already held by another
// process. The wrapped error includes the database path so the operator knows
// which file is contended.
var ErrBusy = errors.New("another bm process is using the database")

// Store is the bm persistence layer.
type Store struct {
	db  *bbolt.DB
	key []byte // 32 bytes; AES-GCM data key
}

// Open returns a Store backed by the bbolt file at path. dataKey must be 32
// bytes — the AES-GCM key used by PutEncrypted/GetEncrypted. If another bm
// process already holds the file lock, Open returns a wrapped ErrBusy after
// the 5s timeout.
func Open(path string, dataKey []byte) (*Store, error) {
	if len(dataKey) != 32 {
		return nil, fmt.Errorf("store: data key must be 32 bytes (got %d)", len(dataKey))
	}
	db, err := bbolt.Open(path, filePerm, &bbolt.Options{Timeout: openTimeout})
	if err != nil {
		if errors.Is(err, bbolt.ErrTimeout) {
			return nil, fmt.Errorf("%w: %s", ErrBusy, path)
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	s := &Store{db: db, key: dataKey}
	if err := s.initBuckets(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) initBuckets() error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		for _, name := range bucketsToCreate {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return fmt.Errorf("create bucket %s: %w", name, err)
			}
		}
		return nil
	})
}

// Close releases the bbolt file lock and underlying handle.
func (s *Store) Close() error { return s.db.Close() }

// Path returns the on-disk database path bbolt is using.
func (s *Store) Path() string { return s.db.Path() }

// View runs fn inside a read-only transaction bounded by a 5s deadline. fn
// MUST NOT call back into the store; doing so deadlocks bbolt.
func (s *Store) View(ctx context.Context, fn func(*bbolt.Tx) error) error {
	return s.runTxn(ctx, false, fn)
}

// Update runs fn inside a writable transaction bounded by a 5s deadline.
func (s *Store) Update(ctx context.Context, fn func(*bbolt.Tx) error) error {
	return s.runTxn(ctx, true, fn)
}

func (s *Store) runTxn(ctx context.Context, write bool, fn func(*bbolt.Tx) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c, cancel := context.WithTimeout(ctx, txnTimeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		if write {
			done <- s.db.Update(fn)
		} else {
			done <- s.db.View(fn)
		}
	}()
	select {
	case err := <-done:
		return err
	case <-c.Done():
		return c.Err()
	}
}

// PutEncrypted seals value with AES-GCM (nonce prefixed) and writes it to
// bucket[key]. Use for buckets that hold secrets (cluster_ssh, cluster_admin).
func (s *Store) PutEncrypted(ctx context.Context, bucket, key, value []byte) error {
	sealed, err := s.seal(value)
	if err != nil {
		return err
	}
	return s.Update(ctx, func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return fmt.Errorf("bucket %s missing", bucket)
		}
		return b.Put(key, sealed)
	})
}

// GetEncrypted reads bucket[key] and unseals it. Returns (nil, nil) when the
// key is absent.
func (s *Store) GetEncrypted(ctx context.Context, bucket, key []byte) ([]byte, error) {
	var sealed []byte
	if err := s.View(ctx, func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucket)
		if b == nil {
			return fmt.Errorf("bucket %s missing", bucket)
		}
		v := b.Get(key)
		if v == nil {
			return nil
		}
		sealed = append(sealed, v...) // copy out — value invalid after txn
		return nil
	}); err != nil {
		return nil, err
	}
	if sealed == nil {
		return nil, nil
	}
	return s.open(sealed)
}

func (s *Store) seal(plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func (s *Store) open(sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(sealed) < ns {
		return nil, errors.New("store: ciphertext shorter than nonce")
	}
	return gcm.Open(nil, sealed[:ns], sealed[ns:], nil)
}
