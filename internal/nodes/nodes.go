// Package nodes provides plain (unencrypted) bbolt CRUD on the `nodes` bucket
// per the layout in docs/manager/backend-design.md. The wire shape is
// domain.Node; bbolt keys are `<clusterId>:<nodeId>`.
package nodes

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

// ErrNotFound is returned by Get when the node is not in bbolt.
var ErrNotFound = errors.New("node not found")

// Repo wraps a *store.Store with node-bucket-specific helpers.
type Repo struct {
	s *store.Store
}

// New returns a Repo against s.
func New(s *store.Store) *Repo { return &Repo{s: s} }

// List returns every node for clusterID, in undefined order. Callers that want
// a stable ordering should sort by hostname/pool client-side.
func (r *Repo) List(ctx context.Context, clusterID string) ([]domain.Node, error) {
	prefix := []byte(clusterID + ":")
	var out []domain.Node
	err := r.s.View(ctx, func(tx *bbolt.Tx) error {
		b := tx.Bucket(store.BucketNodes)
		if b == nil {
			return fmt.Errorf("nodes bucket missing")
		}
		c := b.Cursor()
		for k, v := c.Seek(prefix); k != nil && hasPrefix(k, prefix); k, v = c.Next() {
			var n domain.Node
			if err := json.Unmarshal(v, &n); err != nil {
				return fmt.Errorf("decode %s: %w", k, err)
			}
			out = append(out, n)
		}
		return nil
	})
	if out == nil {
		out = []domain.Node{}
	}
	return out, err
}

// Get returns one node by id. ErrNotFound when absent.
func (r *Repo) Get(ctx context.Context, clusterID, nodeID string) (domain.Node, error) {
	var out domain.Node
	err := r.s.View(ctx, func(tx *bbolt.Tx) error {
		b := tx.Bucket(store.BucketNodes)
		if b == nil {
			return fmt.Errorf("nodes bucket missing")
		}
		raw := b.Get(key(clusterID, nodeID))
		if raw == nil {
			return ErrNotFound
		}
		return json.Unmarshal(raw, &out)
	})
	return out, err
}

// Put inserts or replaces n. clusterId+id are inferred from the node fields;
// callers must set both before calling Put.
func (r *Repo) Put(ctx context.Context, n domain.Node) error {
	if n.ClusterID == "" || n.ID == "" {
		return errors.New("nodes: clusterId and id required")
	}
	body, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return r.s.Update(ctx, func(tx *bbolt.Tx) error {
		b := tx.Bucket(store.BucketNodes)
		if b == nil {
			return fmt.Errorf("nodes bucket missing")
		}
		return b.Put(key(n.ClusterID, n.ID), body)
	})
}

// Delete removes one node. Silent when the row doesn't exist.
func (r *Repo) Delete(ctx context.Context, clusterID, nodeID string) error {
	return r.s.Update(ctx, func(tx *bbolt.Tx) error {
		b := tx.Bucket(store.BucketNodes)
		if b == nil {
			return fmt.Errorf("nodes bucket missing")
		}
		return b.Delete(key(clusterID, nodeID))
	})
}

func key(clusterID, nodeID string) []byte {
	return []byte(clusterID + ":" + nodeID)
}

func hasPrefix(k, prefix []byte) bool {
	return len(k) >= len(prefix) && string(k[:len(prefix)]) == string(prefix) && !strings.ContainsRune(string(k[len(prefix):]), 0)
}
