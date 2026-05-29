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
	// 4 mounts in fixture: /, /boot, /data/disk1, /data/disk2.
	if len(got.Drives) != 4 {
		t.Fatalf("drives: want 4, got %d (%+v)", len(got.Drives), got.Drives)
	}
	var dataMounts []string
	for _, d := range got.Drives {
		if d.IsBoot || d.IsSystem {
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

func TestParseMountInfoDrives(t *testing.T) {
	mountInfo := `36 35 8:1 / / rw,relatime - xfs /dev/sda2 rw,attr2,inode64
37 36 8:2 /boot /boot rw,relatime - xfs /dev/sda1 rw,attr2,inode64
38 36 7:0 / /data/my\040drive rw,relatime - xfs /dev/loop0 rw,attr2,inode64
39 36 0:44 / /var/lib/buckit rw,relatime - xfs /dev/loop1 rw,attr2,inode64
`
	sizes := map[string]int64{
		"/":               107374182400,
		"/boot":           2147483648,
		"/data/my drive":  1099511627776,
		"/var/lib/buckit": 1099511627776,
	}
	got := parseMountInfoDrives(mountInfo, sizes)
	if len(got) != 4 {
		t.Fatalf("want 4 drives, got %d", len(got))
	}
	if got[2].Mount != "/data/my drive" {
		t.Fatalf("unescape mountpoint: got %q", got[2].Mount)
	}
	if got[2].SizeBytes != 1099511627776 {
		t.Fatalf("size for spaced mount: got %d", got[2].SizeBytes)
	}
	if !got[3].IsSystem {
		t.Fatalf("system mount should be flagged")
	}
}

func TestParseMountSizesHandlesSpacedMountpoints(t *testing.T) {
	got := parseMountSizes(`Mounted on 1B-blocks
/ 107374182400
/data/my drive 1099511627776
/mnt/fast pool/drive1 2147483648
`)
	if got["/data/my drive"] != 1099511627776 {
		t.Fatalf("size for /data/my drive: got %d", got["/data/my drive"])
	}
	if got["/mnt/fast pool/drive1"] != 2147483648 {
		t.Fatalf("size for /mnt/fast pool/drive1: got %d", got["/mnt/fast pool/drive1"])
	}
}
