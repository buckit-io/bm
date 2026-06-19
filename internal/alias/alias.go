// Package alias bridges the bm cluster store to the on-disk config file the
// forked bm-cli reads. Every cluster create/update/delete should be followed
// by a Sync call so `bm alias list`, `bm admin info <alias>`, and friends see
// the same view of the world bm web does.
package alias

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/buckit-io/bm/internal/store"
	"go.etcd.io/bbolt"
)

// File version matches bm-cli's configV10 layout. Bumping this here requires
// the matching bump on the bm-cli side.
const configFileVersion = "10"

// FileMode is the on-disk mode for the config file. mc and bm-cli both expect 0600.
const FileMode = 0o600

type aliasEntry struct {
	URL       string `json:"url"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	API       string `json:"api"`
	Path      string `json:"path"`
}

type configFile struct {
	Version string                `json:"version"`
	Aliases map[string]aliasEntry `json:"aliases"`
}

// Sync writes the alias file to path mode 0600 from the current state of the
// store. Each alias joins the cluster's canonical ID (from the plaintext
// `clusters` bucket) with its URL + admin credentials (from the AES-GCM
// encrypted `cluster_admin` bucket, keyed by the same cluster ID). M1 has no
// clusters yet, so the typical output is the empty skeleton — callers should
// still invoke Sync at startup so downstream bm-cli reads don't fail on a
// missing file.
func Sync(ctx context.Context, s *store.Store, path string) error {
	cfg := configFile{
		Version: configFileVersion,
		Aliases: map[string]aliasEntry{},
	}

	// Collect cluster IDs inside a read txn, then fetch the encrypted creds
	// outside it — GetEncrypted opens its own txn to unseal.
	var clusterIDs []string
	if err := s.View(ctx, func(tx *bbolt.Tx) error {
		b := tx.Bucket(store.BucketClusters)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var entry struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(v, &entry); err != nil {
				return fmt.Errorf("alias: decode cluster %s: %w", k, err)
			}
			if entry.ID == "" {
				return nil
			}
			clusterIDs = append(clusterIDs, entry.ID)
			return nil
		})
	}); err != nil {
		return err
	}

	for _, clusterID := range clusterIDs {
		raw, err := s.GetEncrypted(ctx, store.BucketClusterAdmin, []byte(clusterID))
		if err != nil {
			return fmt.Errorf("alias: read admin creds for %s: %w", clusterID, err)
		}
		if raw == nil {
			// No admin creds — can't form a usable alias. Skip.
			continue
		}
		var creds struct {
			URL       string `json:"url"`
			AccessKey string `json:"accessKey"`
			SecretKey string `json:"secretKey"`
		}
		if err := json.Unmarshal(raw, &creds); err != nil {
			return fmt.Errorf("alias: decode admin creds for %s: %w", clusterID, err)
		}
		if creds.URL == "" {
			continue
		}
		cfg.Aliases[clusterID] = aliasEntry{
			URL:       creds.URL,
			AccessKey: creds.AccessKey,
			SecretKey: creds.SecretKey,
			API:       "S3v4",
			Path:      "auto",
		}
	}

	body, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return fmt.Errorf("alias: marshal: %w", err)
	}
	// Write to a tmpfile and rename to make the swap atomic on POSIX.
	tmp, err := os.CreateTemp(dirOf(path), "config-*.json")
	if err != nil {
		return fmt.Errorf("alias: tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("alias: write: %w", err)
	}
	if err := tmp.Chmod(FileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("alias: chmod: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("alias: close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("alias: rename: %w", err)
	}
	return nil
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			if i == 0 {
				return path[:1]
			}
			return path[:i]
		}
	}
	return "."
}
