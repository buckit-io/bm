package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.etcd.io/bbolt"

	"github.com/buckit-io/bm/internal/store"
)

const historyRowCap = 1000

// HistoryFilter narrows down List results. All fields are optional.
type HistoryFilter struct {
	Status    OperationState
	ClusterID string
	Since     time.Time
	Until     time.Time
	Limit     int
}

// ErrHistoryNotFound is returned by Get/Update when the id is missing.
var ErrHistoryNotFound = errors.New("history entry not found")

// historyStore is a thin wrapper used by the orchestrator + API handlers.
type historyStore struct {
	s *store.Store
}

func newHistoryStore(s *store.Store) *historyStore { return &historyStore{s: s} }

// Insert writes entry under entry.ID. Returns an error if a row with that id
// already exists.
func (h *historyStore) Insert(ctx context.Context, entry HistoryEntry) error {
	if entry.ID == "" {
		return fmt.Errorf("history: ID required")
	}
	body, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return h.s.Update(ctx, func(tx *bbolt.Tx) error {
		b := tx.Bucket(store.BucketHistory)
		if b == nil {
			return fmt.Errorf("history bucket missing")
		}
		if existing := b.Get([]byte(entry.ID)); existing != nil {
			return fmt.Errorf("history: %s already exists", entry.ID)
		}
		return b.Put([]byte(entry.ID), body)
	})
}

// Update applies mutator to the existing entry. mutator may modify any field;
// the id is preserved.
func (h *historyStore) Update(ctx context.Context, id string, mutator func(*HistoryEntry)) error {
	return h.s.Update(ctx, func(tx *bbolt.Tx) error {
		b := tx.Bucket(store.BucketHistory)
		if b == nil {
			return fmt.Errorf("history bucket missing")
		}
		raw := b.Get([]byte(id))
		if raw == nil {
			return ErrHistoryNotFound
		}
		var entry HistoryEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			return fmt.Errorf("history: decode %s: %w", id, err)
		}
		mutator(&entry)
		entry.ID = id
		body, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		return b.Put([]byte(id), body)
	})
}

// Get returns the entry by id or ErrHistoryNotFound.
func (h *historyStore) Get(ctx context.Context, id string) (HistoryEntry, error) {
	var out HistoryEntry
	err := h.s.View(ctx, func(tx *bbolt.Tx) error {
		b := tx.Bucket(store.BucketHistory)
		if b == nil {
			return fmt.Errorf("history bucket missing")
		}
		raw := b.Get([]byte(id))
		if raw == nil {
			return ErrHistoryNotFound
		}
		return json.Unmarshal(raw, &out)
	})
	return out, err
}

// List returns rows newest-first (reverse ULID order). Limit 0 means unlimited.
func (h *historyStore) List(ctx context.Context, f HistoryFilter) ([]HistoryEntry, error) {
	var out []HistoryEntry
	err := h.s.View(ctx, func(tx *bbolt.Tx) error {
		b := tx.Bucket(store.BucketHistory)
		if b == nil {
			return fmt.Errorf("history bucket missing")
		}
		c := b.Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			var entry HistoryEntry
			if err := json.Unmarshal(v, &entry); err != nil {
				continue // skip malformed
			}
			if !f.matches(&entry) {
				continue
			}
			out = append(out, entry)
			if f.Limit > 0 && len(out) >= f.Limit {
				return nil
			}
		}
		return nil
	})
	return out, err
}

// Clear deletes rows. If before.IsZero(), wipes everything; otherwise wipes
// rows whose At is strictly before that timestamp.
func (h *historyStore) Clear(ctx context.Context, before time.Time) (int, error) {
	var deleted int
	err := h.s.Update(ctx, func(tx *bbolt.Tx) error {
		b := tx.Bucket(store.BucketHistory)
		if b == nil {
			return fmt.Errorf("history bucket missing")
		}
		toDel := make([][]byte, 0)
		err := b.ForEach(func(k, v []byte) error {
			if before.IsZero() {
				toDel = append(toDel, append([]byte(nil), k...))
				return nil
			}
			var entry HistoryEntry
			if err := json.Unmarshal(v, &entry); err != nil {
				return nil
			}
			if entry.At.Before(before) {
				toDel = append(toDel, append([]byte(nil), k...))
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, k := range toDel {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		deleted = len(toDel)
		return nil
	})
	return deleted, err
}

// Sweep deletes the oldest rows until the bucket size is at most historyRowCap.
func (h *historyStore) Sweep(ctx context.Context) (int, error) {
	var deleted int
	err := h.s.Update(ctx, func(tx *bbolt.Tx) error {
		b := tx.Bucket(store.BucketHistory)
		if b == nil {
			return fmt.Errorf("history bucket missing")
		}
		count := b.Stats().KeyN
		if count <= historyRowCap {
			return nil
		}
		toDel := count - historyRowCap
		c := b.Cursor()
		for k, _ := c.First(); k != nil && deleted < toDel; k, _ = c.Next() {
			key := append([]byte(nil), k...)
			if err := b.Delete(key); err != nil {
				return err
			}
			deleted++
		}
		return nil
	})
	return deleted, err
}

func (f HistoryFilter) matches(e *HistoryEntry) bool {
	if f.Status != "" && e.Status != f.Status {
		return false
	}
	if f.ClusterID != "" && e.ClusterID != f.ClusterID {
		return false
	}
	if !f.Since.IsZero() && e.At.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && e.At.After(f.Until) {
		return false
	}
	return true
}
