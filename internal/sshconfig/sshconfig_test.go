package sshconfig

import (
	"context"
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"

	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/store"
)

func newFixture(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	s, err := store.Open(filepath.Join(dir, "bm.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return New(s)
}

func TestPutGetRoundTrip(t *testing.T) {
	r := newFixture(t)
	ctx := context.Background()
	cfg := domain.ClusterSshConfig{
		SSH: domain.SshCreds{
			AuthMethod: domain.AuthKey, User: "buckit", KeyPath: "/home/ops/.ssh/id_ed25519", Sudo: true,
		},
		Overrides: map[string]domain.SshOverrides{},
	}
	if err := r.Put(ctx, "c1", cfg); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.SSH.User != "buckit" || got.SSH.KeyPath != "/home/ops/.ssh/id_ed25519" || !got.SSH.Sudo {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestGetMissing(t *testing.T) {
	r := newFixture(t)
	_, err := r.Get(context.Background(), "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestPutValidationKeyAuthMissingKeyPath(t *testing.T) {
	r := newFixture(t)
	err := r.Put(context.Background(), "c", domain.ClusterSshConfig{
		SSH:       domain.SshCreds{AuthMethod: domain.AuthKey, User: "u"},
		Overrides: map[string]domain.SshOverrides{},
	})
	if err == nil {
		t.Fatal("expected validation error for missing keyPath")
	}
}

func TestPutInMemory_GetReadsMemoryBeforeDisk(t *testing.T) {
	r := newFixture(t)
	ctx := context.Background()
	disk := domain.ClusterSshConfig{
		SSH: domain.SshCreds{
			AuthMethod: domain.AuthAgent, User: "root",
		},
		Overrides: map[string]domain.SshOverrides{},
	}
	if err := r.Put(ctx, "c1", disk); err != nil {
		t.Fatal(err)
	}
	mem := domain.ClusterSshConfig{
		SSH: domain.SshCreds{
			AuthMethod: domain.AuthAgent, User: "ephemeral",
		},
	}
	if err := r.PutInMemory("c1", mem); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.SSH.User != "ephemeral" {
		t.Fatalf("Get returned disk user %q; want in-memory user 'ephemeral'", got.SSH.User)
	}
}

func TestPutInMemory_GetFallsBackToDiskWhenAbsent(t *testing.T) {
	r := newFixture(t)
	ctx := context.Background()
	disk := domain.ClusterSshConfig{
		SSH: domain.SshCreds{
			AuthMethod: domain.AuthAgent, User: "root",
		},
	}
	if err := r.Put(ctx, "c1", disk); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.SSH.User != "root" {
		t.Fatalf("want disk user 'root', got %q", got.SSH.User)
	}
}

func TestPutPersistentClearsInMemory(t *testing.T) {
	r := newFixture(t)
	ctx := context.Background()
	if err := r.PutInMemory("c1", domain.ClusterSshConfig{
		SSH: domain.SshCreds{AuthMethod: domain.AuthAgent, User: "ephemeral"},
	}); err != nil {
		t.Fatal(err)
	}
	disk := domain.ClusterSshConfig{
		SSH: domain.SshCreds{AuthMethod: domain.AuthAgent, User: "persistent"},
	}
	if err := r.Put(ctx, "c1", disk); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.SSH.User != "persistent" {
		t.Fatalf("persistent Put should drop the in-memory entry; got user %q", got.SSH.User)
	}
}

func TestOverrideValidation(t *testing.T) {
	r := newFixture(t)
	method := domain.AuthPassword
	err := r.Put(context.Background(), "c", domain.ClusterSshConfig{
		SSH: domain.SshCreds{AuthMethod: domain.AuthKey, User: "u", KeyPath: "/k"},
		Overrides: map[string]domain.SshOverrides{
			"h1": {AuthMethod: &method},
		},
	})
	if err == nil {
		t.Fatal("expected validation error for override switching to password without setting password")
	}
}
