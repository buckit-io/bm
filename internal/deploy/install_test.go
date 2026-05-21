package deploy

import (
	"strings"
	"testing"

	"github.com/buckit-io/bm/internal/domain"
)

func TestRenderConfigEnvUsesManagedStorageSubdirs(t *testing.T) {
	params := DeployParams{
		Credentials: domain.Credentials{RootUser: "root", RootPassword: "secret"},
		API:         domain.APIPorts{Port: 9000, ConsolePort: 9001},
		Region:      "us-east-1",
		Hosts:       []domain.HostRow{{Hostname: "node1"}},
		Topology:    domain.Topology{SelectedMounts: []string{"/data/drive0", "/data/drive1"}},
	}

	got := renderConfigEnv(params, domain.HostRow{Hostname: "node1"})
	if !strings.Contains(got, `MINIO_VOLUMES="/data/drive{0...1}/buckit"`) {
		t.Fatalf("renderConfigEnv() missing compressed managed storage paths:\n%s", got)
	}
}

func TestRenderConfigEnvUsesHostAndDriveExpansionWhenPossible(t *testing.T) {
	params := DeployParams{
		Credentials: domain.Credentials{RootUser: "root", RootPassword: "secret"},
		API:         domain.APIPorts{Port: 9000, ConsolePort: 9001},
		Region:      "us-east-1",
		Hosts: []domain.HostRow{
			{Hostname: "node1.example.com"},
			{Hostname: "node2.example.com"},
			{Hostname: "node3.example.com"},
			{Hostname: "node4.example.com"},
		},
		Topology: domain.Topology{SelectedMounts: []string{"/data/drive0", "/data/drive1"}},
	}

	got := renderConfigEnv(params, domain.HostRow{Hostname: "node1.example.com"})
	want := `MINIO_VOLUMES="http://node{1...4}.example.com:9000/data/drive{0...1}/buckit"`
	if !strings.Contains(got, want) {
		t.Fatalf("renderConfigEnv() missing compressed distributed volumes:\n%s", got)
	}
}

func TestRenderConfigEnvFallsBackToExplicitHostsWhenHostnamePatternDoesNotFit(t *testing.T) {
	params := DeployParams{
		Credentials: domain.Credentials{RootUser: "root", RootPassword: "secret"},
		API:         domain.APIPorts{Port: 9000, ConsolePort: 9001},
		Region:      "us-east-1",
		Hosts: []domain.HostRow{
			{Hostname: "alpha"},
			{Hostname: "beta"},
		},
		Topology: domain.Topology{SelectedMounts: []string{"/data/drive0", "/data/drive1"}},
	}

	got := renderConfigEnv(params, domain.HostRow{Hostname: "alpha"})
	want := `MINIO_VOLUMES="http://alpha:9000/data/drive{0...1}/buckit http://beta:9000/data/drive{0...1}/buckit"`
	if !strings.Contains(got, want) {
		t.Fatalf("renderConfigEnv() missing explicit-host fallback:\n%s", got)
	}
}

func TestPrepareStorageCmdTargetsManagedStorageSubdirs(t *testing.T) {
	got := prepareStorageCmd([]string{"/data/drive0"})
	if !strings.Contains(got, "mkdir -p /data/drive0/buckit\n") {
		t.Fatalf("prepareStorageCmd() missing managed subdir mkdir:\n%s", got)
	}
	if strings.Contains(got, "chown buckit:buckit /data/drive0\n") {
		t.Fatalf("prepareStorageCmd() should not chown the mount root:\n%s", got)
	}
	if !strings.Contains(got, "chown buckit:buckit /data/drive0/buckit\n") {
		t.Fatalf("prepareStorageCmd() missing subdir chown:\n%s", got)
	}
}
