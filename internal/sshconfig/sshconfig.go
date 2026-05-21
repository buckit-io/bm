// Package sshconfig stores per-cluster SSH credentials in the AES-GCM
// encrypted `cluster_ssh` bbolt bucket. Decryption happens on read so callers
// never see ciphertext.
package sshconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go.etcd.io/bbolt"

	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/store"
)

// ErrNotFound is returned by Get when the cluster has no SSH config yet.
var ErrNotFound = errors.New("ssh config not found")

// Repo wraps a store.Store with cluster_ssh helpers.
type Repo struct {
	s *store.Store
}

// New returns a Repo against s.
func New(s *store.Store) *Repo { return &Repo{s: s} }

// Get returns the decrypted ClusterSshConfig for clusterID. ErrNotFound when absent.
func (r *Repo) Get(ctx context.Context, clusterID string) (domain.ClusterSshConfig, error) {
	raw, err := r.s.GetEncrypted(ctx, store.BucketClusterSSH, []byte(clusterID))
	if err != nil {
		return domain.ClusterSshConfig{}, err
	}
	if raw == nil {
		return domain.ClusterSshConfig{}, ErrNotFound
	}
	var cfg domain.ClusterSshConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return domain.ClusterSshConfig{}, fmt.Errorf("sshconfig: decode %s: %w", clusterID, err)
	}
	if cfg.Overrides == nil {
		cfg.Overrides = map[string]domain.SshOverrides{}
	}
	return cfg, nil
}

// Put validates + encrypts cfg and writes it under clusterID. Caller is
// responsible for invalidating any pooled SSH clients (via ssh.Pool.Drop) when
// credentials change.
func (r *Repo) Put(ctx context.Context, clusterID string, cfg domain.ClusterSshConfig) error {
	if err := Validate(cfg); err != nil {
		return err
	}
	body, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("sshconfig: marshal: %w", err)
	}
	return r.s.PutEncrypted(ctx, store.BucketClusterSSH, []byte(clusterID), body)
}

// Delete drops the cluster's SSH config.
func (r *Repo) Delete(ctx context.Context, clusterID string) error {
	return r.s.Update(ctx, func(tx *bbolt.Tx) error {
		b := tx.Bucket(store.BucketClusterSSH)
		if b == nil {
			return fmt.Errorf("cluster_ssh bucket missing")
		}
		return b.Delete([]byte(clusterID))
	})
}

// Validate enforces the same rules the UI's wizard enforces client-side:
// auth-method-specific required fields, user always set.
func Validate(cfg domain.ClusterSshConfig) error {
	if err := validateCreds(cfg.SSH); err != nil {
		return fmt.Errorf("sshconfig: cluster default: %w", err)
	}
	for hostID, ov := range cfg.Overrides {
		if err := validateOverride(ov); err != nil {
			return fmt.Errorf("sshconfig: override %s: %w", hostID, err)
		}
	}
	return nil
}

func validateCreds(c domain.SshCreds) error {
	if c.User == "" {
		return errors.New("user is required")
	}
	switch c.AuthMethod {
	case domain.AuthKey:
		if c.KeyPath == "" {
			return errors.New("keyPath required for key auth")
		}
	case domain.AuthPassword:
		if c.Password == "" {
			return errors.New("password required for password auth")
		}
	case domain.AuthAgent:
		// no extra required fields
	default:
		return fmt.Errorf("unsupported authMethod %q", c.AuthMethod)
	}
	return nil
}

func validateOverride(o domain.SshOverrides) error {
	if o.AuthMethod == nil {
		return nil // not overriding method; inherit
	}
	switch *o.AuthMethod {
	case domain.AuthKey:
		if o.KeyPath == nil || *o.KeyPath == "" {
			return errors.New("keyPath required when overriding to key auth")
		}
	case domain.AuthPassword:
		if o.Password == nil || *o.Password == "" {
			return errors.New("password required when overriding to password auth")
		}
	case domain.AuthAgent:
		// fine
	default:
		return fmt.Errorf("unsupported authMethod %q", *o.AuthMethod)
	}
	return nil
}
