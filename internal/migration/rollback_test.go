package migration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/tasks"
)

// TestRollbackExecutorReverts confirms a successful cutover can be reversed:
// the executor runs Installer.Rollback per host, and the cluster row's
// Engine flips back to EngineMinio with MigratedFrom cleared.
func TestRollbackExecutorReverts(t *testing.T) {
	fix := newCutoverFixture(t, 1)
	defer fix.cleanup()

	// Pretend a cutover already completed — cluster row says EngineBuckit.
	now := time.Now().UTC()
	if err := fix.clusters.Put(context.Background(), domain.Cluster{
		ID:             fix.clusterID,
		Name:           "test",
		Engine:         domain.EngineBuckit,
		Version:        "v1.0.0",
		NodeCount:      1,
		PoolCount:      1,
		LastActivityAt: now,
		CreatedAt:      now,
		MigratedFrom: &domain.MigratedFrom{
			Product:     "minio",
			Version:     "RELEASE.2026-01-01T00-00-00Z",
			FinalizedAt: now,
		},
	}); err != nil {
		t.Fatal(err)
	}

	exec := &RollbackExecutor{
		Installer:    NewInstaller(fix.sshPool),
		Clusters:     fix.clusters,
		ClusterAdmin: fix.clusterAdmin,
		AdminPool:    fix.adminPool,
		SSHPool:      fix.sshPool,
	}
	body := fix.body()
	raw, _ := json.Marshal(body)
	req := tasks.DispatchRequest{
		ClusterID: fix.clusterID,
		Kind:      tasks.OpMigrateRollback,
		Params:    raw,
	}
	if err := exec.Validate(req); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	hub := tasks.NewHub("rollback-task")
	run := &tasks.Run{
		TaskID:    "rollback-task",
		ClusterID: fix.clusterID,
		Kind:      tasks.OpMigrateRollback,
		Params:    raw,
		Hub:       hub,
		Store:     fix.store,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := exec.Execute(ctx, run); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	c, err := fix.clusters.Get(context.Background(), fix.clusterID)
	if err != nil {
		t.Fatalf("Get cluster: %v", err)
	}
	if c.Engine != domain.EngineMinio {
		t.Fatalf("Engine: want minio, got %s", c.Engine)
	}
	if c.MigratedFrom != nil {
		t.Fatalf("MigratedFrom should be cleared, got %+v", c.MigratedFrom)
	}

	state := hub.Snapshot()
	foundCount := false
	for _, item := range state.Summary {
		if item.Label == "Rolled back" && item.Value == "1" {
			foundCount = true
		}
	}
	if !foundCount {
		t.Fatalf("missing 'Rolled back' summary item: %+v", state.Summary)
	}
}

// TestRollbackExecutorNoOpWhenAlreadyMinio confirms hosts whose
// buckit.service probes as inactive are skipped: no per-host work runs
// and the cluster row's Engine is not flipped.
func TestRollbackExecutorNoOpWhenAlreadyMinio(t *testing.T) {
	fix := newCutoverFixture(t, 1)
	defer fix.cleanup()

	// Override the SSH probe so `systemctl is-active buckit.service` reports
	// inactive. Every other command falls through to the default table.
	fix.sshSrv.CmdOverride = func(cmd string) (string, string, int, bool) {
		if strings.HasPrefix(cmd, "systemctl is-active buckit") {
			return "inactive\n", "", 3, true
		}
		return "", "", 0, false
	}

	// Cluster currently still on Buckit.
	now := time.Now().UTC()
	if err := fix.clusters.Put(context.Background(), domain.Cluster{
		ID:             fix.clusterID,
		Name:           "test",
		Engine:         domain.EngineBuckit,
		Version:        "v1.0.0",
		NodeCount:      1,
		PoolCount:      1,
		LastActivityAt: now,
		CreatedAt:      now,
	}); err != nil {
		t.Fatal(err)
	}

	exec := &RollbackExecutor{
		Installer:    NewInstaller(fix.sshPool),
		Clusters:     fix.clusters,
		ClusterAdmin: fix.clusterAdmin,
		AdminPool:    fix.adminPool,
		SSHPool:      fix.sshPool,
	}
	body := fix.body()
	raw, _ := json.Marshal(body)
	hub := tasks.NewHub("noop-task")
	run := &tasks.Run{
		TaskID:    "noop-task",
		ClusterID: fix.clusterID,
		Kind:      tasks.OpMigrateRollback,
		Params:    raw,
		Hub:       hub,
		Store:     fix.store,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := exec.Execute(ctx, run); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	c, _ := fix.clusters.Get(context.Background(), fix.clusterID)
	if c.Engine != domain.EngineBuckit {
		t.Fatalf("Engine should still be buckit (no-op rollback), got %s", c.Engine)
	}
	state := hub.Snapshot()
	foundSkip := false
	for _, hs := range state.HostStatuses {
		if hs.Detail == "Already on MinIO" {
			foundSkip = true
		}
	}
	if !foundSkip {
		t.Fatalf("expected 'Already on MinIO' host detail, got %+v", state.HostStatuses)
	}
}
