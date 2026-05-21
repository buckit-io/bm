// Package clusters provides plain (unencrypted) bbolt CRUD on the `clusters`
// bucket plus the health-rollup helpers the discovery and refresh paths use.
//
// Delete is a cascade: clusters, nodes, ssh config, and admin creds for the
// cluster all go away. History rows are intentionally retained so the
// History page keeps rendering past activity even after the cluster
// definition is removed.
package clusters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.etcd.io/bbolt"

	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/store"
)

// ErrNotFound is returned by Get when the cluster id isn't in bbolt.
var ErrNotFound = errors.New("cluster not found")

// Repo wraps a *store.Store with cluster-bucket-specific helpers.
type Repo struct {
	s *store.Store
}

// New returns a Repo against s.
func New(s *store.Store) *Repo { return &Repo{s: s} }

// List returns every persisted cluster in undefined order.
func (r *Repo) List(ctx context.Context) ([]domain.Cluster, error) {
	var out []domain.Cluster
	err := r.s.View(ctx, func(tx *bbolt.Tx) error {
		b := tx.Bucket(store.BucketClusters)
		if b == nil {
			return fmt.Errorf("clusters bucket missing")
		}
		return b.ForEach(func(_, v []byte) error {
			var c domain.Cluster
			if err := json.Unmarshal(v, &c); err != nil {
				return nil // skip malformed rows
			}
			out = append(out, c)
			return nil
		})
	})
	if out == nil {
		out = []domain.Cluster{}
	}
	return out, err
}

// Get returns one cluster by id. ErrNotFound when absent.
func (r *Repo) Get(ctx context.Context, id string) (domain.Cluster, error) {
	var out domain.Cluster
	err := r.s.View(ctx, func(tx *bbolt.Tx) error {
		b := tx.Bucket(store.BucketClusters)
		if b == nil {
			return fmt.Errorf("clusters bucket missing")
		}
		raw := b.Get([]byte(id))
		if raw == nil {
			return ErrNotFound
		}
		return json.Unmarshal(raw, &out)
	})
	return out, err
}

// Put inserts or replaces c. c.ID must be set.
func (r *Repo) Put(ctx context.Context, c domain.Cluster) error {
	if c.ID == "" {
		return errors.New("clusters: id required")
	}
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return r.s.Update(ctx, func(tx *bbolt.Tx) error {
		b := tx.Bucket(store.BucketClusters)
		if b == nil {
			return fmt.Errorf("clusters bucket missing")
		}
		return b.Put([]byte(c.ID), body)
	})
}

// Exists reports whether id is persisted. Cheaper than Get when callers only
// care about existence (e.g., import-commit slug collision checks).
func (r *Repo) Exists(ctx context.Context, id string) (bool, error) {
	var found bool
	err := r.s.View(ctx, func(tx *bbolt.Tx) error {
		b := tx.Bucket(store.BucketClusters)
		if b == nil {
			return fmt.Errorf("clusters bucket missing")
		}
		found = b.Get([]byte(id)) != nil
		return nil
	})
	return found, err
}

// Delete removes the cluster row and every dependent row (nodes, ssh config,
// admin creds). History rows are retained — see package comment.
func (r *Repo) Delete(ctx context.Context, id string) error {
	return r.s.Update(ctx, func(tx *bbolt.Tx) error {
		// clusters
		if b := tx.Bucket(store.BucketClusters); b != nil {
			if err := b.Delete([]byte(id)); err != nil {
				return err
			}
		}
		// nodes — keyed `<clusterId>:<nodeId>`
		if b := tx.Bucket(store.BucketNodes); b != nil {
			prefix := []byte(id + ":")
			c := b.Cursor()
			var toDel [][]byte
			for k, _ := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, _ = c.Next() {
				toDel = append(toDel, append([]byte(nil), k...))
			}
			for _, k := range toDel {
				if err := b.Delete(k); err != nil {
					return err
				}
			}
		}
		// node_facts
		if b := tx.Bucket(store.BucketNodeFacts); b != nil {
			prefix := []byte(id + ":")
			c := b.Cursor()
			var toDel [][]byte
			for k, _ := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, _ = c.Next() {
				toDel = append(toDel, append([]byte(nil), k...))
			}
			for _, k := range toDel {
				if err := b.Delete(k); err != nil {
					return err
				}
			}
		}
		// cluster_ssh and cluster_admin — keyed by clusterId directly
		for _, bucket := range [][]byte{store.BucketClusterSSH, store.BucketClusterAdmin} {
			if b := tx.Bucket(bucket); b != nil {
				if err := b.Delete([]byte(id)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
