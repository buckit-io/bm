// Package clusteradmin stores per-cluster admin API credentials (URL +
// access key + secret key + insecure flag) in the AES-GCM encrypted
// `cluster_admin` bbolt bucket. Decryption happens on read so callers never
// see ciphertext.
package clusteradmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/store"
)

// ErrNotFound is returned by Get when the cluster has no admin creds.
var ErrNotFound = errors.New("admin creds not found")

// Repo wraps a *store.Store with cluster_admin helpers.
type Repo struct {
	s *store.Store
}

// New returns a Repo against s.
func New(s *store.Store) *Repo { return &Repo{s: s} }

// Get returns the decrypted creds for clusterID. ErrNotFound when absent.
func (r *Repo) Get(ctx context.Context, clusterID string) (domain.AdminCreds, error) {
	raw, err := r.s.GetEncrypted(ctx, store.BucketClusterAdmin, []byte(clusterID))
	if err != nil {
		return domain.AdminCreds{}, err
	}
	if raw == nil {
		return domain.AdminCreds{}, ErrNotFound
	}
	var c domain.AdminCreds
	if err := json.Unmarshal(raw, &c); err != nil {
		return domain.AdminCreds{}, fmt.Errorf("clusteradmin: decode %s: %w", clusterID, err)
	}
	return c, nil
}

// Put validates and encrypts creds, then writes them under clusterID.
func (r *Repo) Put(ctx context.Context, clusterID string, creds domain.AdminCreds) error {
	if err := validate(creds); err != nil {
		return err
	}
	body, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("clusteradmin: marshal: %w", err)
	}
	return r.s.PutEncrypted(ctx, store.BucketClusterAdmin, []byte(clusterID), body)
}

// Delete is intentionally absent — cluster removal is a cascade owned by
// internal/clusters.

func validate(c domain.AdminCreds) error {
	if c.URL == "" {
		return errors.New("clusteradmin: url required")
	}
	if c.AccessKey == "" {
		return errors.New("clusteradmin: accessKey required")
	}
	if c.SecretKey == "" {
		return errors.New("clusteradmin: secretKey required")
	}
	return nil
}
