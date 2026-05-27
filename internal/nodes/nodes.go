// Package nodes provides plain (unencrypted) bbolt CRUD on the `nodes` bucket
// per the layout in docs/manager/backend-design.md. The wire shape is
// domain.Node; bbolt keys are `<clusterId>:<nodeId>`.
package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

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

// List returns every node for clusterID, sorted by hostname in
// numeric-aware order (minio2 < minio10). The repo enforces a stable
// order at the data-access boundary so callers — the migrate wizard, the
// cluster detail page, operation executors — don't have to. Callers that
// need a different sort (e.g. the detail page's pool-aware compareNodes)
// re-sort the returned slice.
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
	sort.SliceStable(out, func(i, j int) bool {
		return collateHostnames(out[i].Hostname, out[j].Hostname) < 0
	})
	return out, err
}

// collateHostnames is a numeric-aware hostname comparator: "minio2"
// sorts before "minio10". Splits each side into runs of digits vs
// non-digits and compares run-by-run — digit runs compared as integers,
// non-digit runs compared lexicographically (case-insensitive). Mirrors
// the front-end's localeCompare(..., {numeric: true}) behavior so the
// order an operator sees in the wizard's host table matches what the
// cluster detail page renders.
func collateHostnames(a, b string) int {
	for len(a) > 0 && len(b) > 0 {
		aDigit := unicode.IsDigit(rune(a[0]))
		bDigit := unicode.IsDigit(rune(b[0]))
		if aDigit != bDigit {
			// Digit run vs alpha run — fall back to byte compare to
			// keep the result deterministic.
			return strings.Compare(a, b)
		}
		aRun, aRest := takeRun(a, aDigit)
		bRun, bRest := takeRun(b, bDigit)
		if aDigit {
			// Compare numerically by trimming leading zeros, then by
			// length, then by string value. ("002" == "2" but sorts
			// stably; "10" > "2".)
			aTrim := strings.TrimLeft(aRun, "0")
			bTrim := strings.TrimLeft(bRun, "0")
			if len(aTrim) != len(bTrim) {
				return len(aTrim) - len(bTrim)
			}
			if c := strings.Compare(aTrim, bTrim); c != 0 {
				return c
			}
		} else {
			aL := strings.ToLower(aRun)
			bL := strings.ToLower(bRun)
			if c := strings.Compare(aL, bL); c != 0 {
				return c
			}
		}
		a, b = aRest, bRest
	}
	return len(a) - len(b)
}

// takeRun splits s into a leading run of digit-or-non-digit chars and
// the remainder, depending on isDigit. Used by collateHostnames.
func takeRun(s string, isDigit bool) (run, rest string) {
	for i := 0; i < len(s); i++ {
		if unicode.IsDigit(rune(s[i])) != isDigit {
			return s[:i], s[i:]
		}
	}
	return s, ""
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
