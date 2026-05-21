package deploy

import (
	"context"
	"testing"

	"github.com/buckit-io/bm/internal/domain"
	bmssh "github.com/buckit-io/bm/internal/ssh"
	"github.com/buckit-io/bm/internal/sshtest"
)

func TestRichProbeAgainstFakeServer(t *testing.T) {
	srv, err := sshtest.Start(sshtest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	host, port := srv.HostPort()
	client, err := bmssh.Dial(context.Background(), domain.HostRef{Hostname: host, Port: port}, bmssh.Resolved{
		AuthMethod: domain.AuthPassword, User: srv.User(), Password: srv.Password(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	got := RichProbe(context.Background(), client)
	if got.State != domain.WizardDiscoveryDone {
		t.Fatalf("state: want done, got %s (err=%s)", got.State, got.Error)
	}
	if got.Arch != "amd64" {
		t.Fatalf("arch: want amd64, got %q", got.Arch)
	}
	if got.Kernel != "6.6.0-test" {
		t.Fatalf("kernel: want 6.6.0-test, got %q", got.Kernel)
	}
	if got.OS != "bm-test 1.0" {
		t.Fatalf("os: want 'bm-test 1.0', got %q", got.OS)
	}
	if got.Cores == nil || *got.Cores != 8 {
		t.Fatalf("cores: want 8, got %v", got.Cores)
	}
	if got.RamGiB == nil || *got.RamGiB != 16 {
		t.Fatalf("ramGiB: want 16, got %v", got.RamGiB)
	}
	if got.NIC != "eth0" {
		t.Fatalf("nic: want eth0, got %q", got.NIC)
	}
	if got.SudoOk == nil || !*got.SudoOk {
		t.Fatalf("sudoOk: want true, got %v", got.SudoOk)
	}
	// 3 disks in fixture: sda (boot), sdb, sdc.
	if len(got.Drives) != 3 {
		t.Fatalf("drives: want 3, got %d (%+v)", len(got.Drives), got.Drives)
	}
	var dataMounts []string
	for _, d := range got.Drives {
		if d.IsBoot {
			continue
		}
		dataMounts = append(dataMounts, d.Mount)
	}
	if len(dataMounts) != 2 {
		t.Fatalf("want 2 data drives, got %v", dataMounts)
	}
}

func TestNormalizeArch(t *testing.T) {
	cases := map[string]string{
		"x86_64":  "amd64",
		"aarch64": "arm64",
		"armv7l":  "arm",
		"weird":   "weird",
	}
	for in, want := range cases {
		if got := normalizeArch(in); got != want {
			t.Errorf("normalizeArch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseMemTotalGiB(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"MemTotal:       16777216 kB\n", 16},
		{"MemTotal:        8388608 kB\n", 8},
		{"MemTotal:               0 kB\n", 0},
		{"NoMatch:        16777216 kB\n", 0},
	}
	for _, tc := range cases {
		if got := parseMemTotalGiB(tc.in); got != tc.want {
			t.Errorf("parseMemTotalGiB(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
