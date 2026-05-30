package alias

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	s, err := store.Open(filepath.Join(t.TempDir(), "bm.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func readConfig(t *testing.T, path string) configFile {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg configFile
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return cfg
}

// A cluster's URL + creds live in the encrypted cluster_admin bucket, not on
// the cluster row. Sync must join the two so the alias is non-empty.
func TestSyncJoinsAdminCreds(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "config.json")

	if err := clusters.New(s).Put(ctx, domain.Cluster{ID: "prod-1", Name: "prod"}); err != nil {
		t.Fatal(err)
	}
	creds := domain.AdminCreds{
		URL:       "https://buckit.example.com:9000",
		AccessKey: "AKIA",
		SecretKey: "shhh",
	}
	if err := clusteradmin.New(s).Put(ctx, "prod-1", creds); err != nil {
		t.Fatal(err)
	}

	if err := Sync(ctx, s, path); err != nil {
		t.Fatal(err)
	}

	cfg := readConfig(t, path)
	got, ok := cfg.Aliases["prod"]
	if !ok {
		t.Fatalf("alias %q missing; aliases=%v", "prod", cfg.Aliases)
	}
	if got.URL != creds.URL || got.AccessKey != creds.AccessKey || got.SecretKey != creds.SecretKey {
		t.Errorf("alias creds mismatch: got %+v want url/keys from %+v", got, creds)
	}
	if got.API != "S3v4" || got.Path != "auto" {
		t.Errorf("alias api/path: got %q/%q want S3v4/auto", got.API, got.Path)
	}
}

// A cluster with no admin creds can't form a usable alias and is skipped
// rather than emitted with an empty URL.
func TestSyncSkipsClusterWithoutCreds(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "config.json")

	if err := clusters.New(s).Put(ctx, domain.Cluster{ID: "bare-1", Name: "bare"}); err != nil {
		t.Fatal(err)
	}
	if err := Sync(ctx, s, path); err != nil {
		t.Fatal(err)
	}

	cfg := readConfig(t, path)
	if len(cfg.Aliases) != 0 {
		t.Errorf("expected no aliases, got %v", cfg.Aliases)
	}
	if cfg.Version != configFileVersion {
		t.Errorf("version: got %q want %q", cfg.Version, configFileVersion)
	}
}
