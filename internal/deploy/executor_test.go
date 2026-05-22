package deploy

import (
	"context"
	"crypto/rand"
	"path/filepath"
	"testing"

	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/domain"
	"github.com/buckit-io/bm/internal/nodes"
	"github.com/buckit-io/bm/internal/sshconfig"
	"github.com/buckit-io/bm/internal/store"
)

func TestCommitPersistsSSHConfig(t *testing.T) {
	dir := t.TempDir()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	st, err := store.Open(filepath.Join(dir, "bm.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	exec := &Executor{
		Clusters:     clusters.New(st),
		Nodes:        nodes.New(st),
		ClusterAdmin: clusteradmin.New(st),
		SSHConfig:    sshconfig.New(st),
	}

	overrideMethod := domain.AuthKey
	overrideUser := "hostops"
	overrideKey := "/Users/tester/.ssh/hostops"
	overrideSudo := true
	params := DeployParams{
		Name: "prod-east",
		Credentials: domain.Credentials{
			RootUser:     "admin",
			RootPassword: "supersecret",
		},
		API: domain.APIPorts{Port: 9000, ConsolePort: 9001},
		ServerURL: "http://prod-east.example.com:9000",
		SSH: domain.SshCreds{
			AuthMethod: domain.AuthPassword,
			User:       "ops",
			Password:   "ssh-secret",
			Sudo:       true,
		},
		Hosts: []domain.HostRow{
			{ID: "host-a", Hostname: "10.0.0.10", Port: 22},
			{
				ID:       "host-b",
				Hostname: "10.0.0.11",
				Port:     2222,
				SSHOverride: &domain.SshOverrides{
					AuthMethod: &overrideMethod,
					User:       &overrideUser,
					KeyPath:    &overrideKey,
					Sudo:       &overrideSudo,
				},
			},
		},
		Topology: domain.Topology{Parity: 2, SelectedMounts: []string{"/data/disk1"}},
	}

	clusterID, err := exec.commit(context.Background(), params, VerifyResult{
		ConsoleURL:   "http://prod-east.example.com:9001",
		Version:      "RELEASE.2026-05-20T21-14-57Z",
		RawBytes:     100,
		UsedBytes:    10,
		UsableBytes:  90,
		NodesHealthy: 2,
		NodesTotal:   2,
		PoolsOnline:  1,
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if clusterID != "prod-east" {
		t.Fatalf("clusterID: want prod-east, got %s", clusterID)
	}

	cfg, err := exec.SSHConfig.Get(context.Background(), clusterID)
	if err != nil {
		t.Fatalf("get ssh config: %v", err)
	}
	if cfg.SSH.User != params.SSH.User || cfg.SSH.Password != params.SSH.Password {
		t.Fatalf("cluster ssh creds not persisted: %+v", cfg.SSH)
	}
	if len(cfg.Overrides) != 1 {
		t.Fatalf("overrides: want 1, got %+v", cfg.Overrides)
	}
	got, ok := cfg.Overrides["prod-east-n2"]
	if !ok {
		t.Fatalf("missing override for prod-east-n2: %+v", cfg.Overrides)
	}
	if got.User == nil || *got.User != overrideUser {
		t.Fatalf("override user: got %+v", got.User)
	}
	if got.KeyPath == nil || *got.KeyPath != overrideKey {
		t.Fatalf("override keyPath: got %+v", got.KeyPath)
	}
}
