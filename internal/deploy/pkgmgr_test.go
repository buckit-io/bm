package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestRPMManagerInspectScriptShape pins the inspect script's
// observable contract: a missing package emits installed="" (and the
// guard against rpm -q printing "package X is not installed" to
// stdout is preserved), and the printf line emits the three keys
// ParseInspectOutput consumes.
func TestRPMManagerInspectScriptShape(t *testing.T) {
	got := rpmManager{}.InspectScript()
	mustContain := []string{
		`rpm -q --quiet buckit`,
		`installed=""`,
		`rpm -qp --qf '%{EPOCHNUM}:%{VERSION}-%{RELEASE}.%{ARCH}\n' /tmp/buckit.rpm`,
		`rpm.vercmp`,
		`printf 'installed=%s\ncandidate=%s\ncmp=%s\n'`,
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("InspectScript missing %q\nfull script:\n%s", want, got)
		}
	}
}

func TestRPMManagerInstallCommands(t *testing.T) {
	t.Run("dnf default", func(t *testing.T) {
		m := NewRPMManager("")
		cases := map[InstallAction]string{
			InstallActionFresh:     "dnf install -y /tmp/buckit.rpm",
			InstallActionUpgrade:   "dnf upgrade -y /tmp/buckit.rpm",
			InstallActionReinstall: "dnf reinstall -y /tmp/buckit.rpm",
		}
		for action, want := range cases {
			if got := m.InstallCommand(action); got != want {
				t.Errorf("dnf %s: got %q want %q", action, got, want)
			}
		}
	})
	t.Run("yum verb", func(t *testing.T) {
		m := NewRPMManager("yum")
		if got := m.InstallCommand(InstallActionUpgrade); got != "yum upgrade -y /tmp/buckit.rpm" {
			t.Errorf("yum upgrade: %q", got)
		}
	})
	t.Run("downgrade refuses", func(t *testing.T) {
		got := NewRPMManager("dnf").InstallCommand(InstallActionDowngrade)
		if !strings.Contains(got, "exit 1") {
			t.Errorf("downgrade should refuse: %q", got)
		}
	})
}

func TestRPMManagerUninstallCommand(t *testing.T) {
	if got := NewRPMManager("dnf").UninstallCommand(); got != "dnf remove -y buckit || true" {
		t.Errorf("dnf uninstall: %q", got)
	}
	if got := NewRPMManager("yum").UninstallCommand(); got != "yum remove -y buckit || true" {
		t.Errorf("yum uninstall: %q", got)
	}
}

func TestDebManagerUninstallCommand(t *testing.T) {
	if got := NewDebManager().UninstallCommand(); got != "apt-get remove -y buckit || true" {
		t.Errorf("apt uninstall: %q", got)
	}
}

func TestRPMManagerDownloadAndVerify(t *testing.T) {
	m := rpmManager{}
	if got := m.DownloadCommand("https://example.test/buckit.rpm"); got != "curl -fSL -o /tmp/buckit.rpm https://example.test/buckit.rpm" {
		t.Errorf("DownloadCommand: %q", got)
	}
	verify := m.VerifyChecksumCommand("aaaa")
	if !strings.Contains(verify, "/tmp/buckit.rpm") {
		t.Errorf("VerifyChecksumCommand: %q", verify)
	}
	if !strings.Contains(verify, "sha256sum -c -") {
		t.Errorf("VerifyChecksumCommand should reference sha256sum: %q", verify)
	}
}

func TestDebManagerScripts(t *testing.T) {
	m := NewDebManager()
	if m.Kind() != "deb" {
		t.Fatalf("Kind: %q", m.Kind())
	}
	if m.LocalFile() != "/tmp/buckit.deb" {
		t.Fatalf("LocalFile: %q", m.LocalFile())
	}
	if got := m.DownloadCommand("https://example.test/buckit.deb"); got != "curl -fSL -o /tmp/buckit.deb https://example.test/buckit.deb" {
		t.Errorf("DownloadCommand: %q", got)
	}
	if got := m.InstallCommand(InstallActionFresh); got != "apt-get install -y /tmp/buckit.deb" {
		t.Errorf("fresh: %q", got)
	}
	if got := m.InstallCommand(InstallActionReinstall); got != "apt-get install -y --reinstall /tmp/buckit.deb" {
		t.Errorf("reinstall: %q", got)
	}
	if got := m.InstallCommand(InstallActionUpgrade); got != "apt-get install -y /tmp/buckit.deb" {
		t.Errorf("upgrade: %q", got)
	}
	script := m.InspectScript()
	for _, want := range []string{"dpkg-query -W -f", "dpkg-deb -f", "dpkg --compare-versions", "/tmp/buckit.deb"} {
		if !strings.Contains(script, want) {
			t.Errorf("InspectScript missing %q\nfull script:\n%s", want, script)
		}
	}
}

func TestParseInspectOutput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want InstallAction
		err  bool
	}{
		{
			name: "fresh install",
			in:   "installed=\ncandidate=1:0.1.0-1.x86_64\ncmp=0\n",
			want: InstallActionFresh,
		},
		{
			name: "same EVR reinstall",
			in:   "installed=1:0.1.0-1.x86_64\ncandidate=1:0.1.0-1.x86_64\ncmp=0\n",
			want: InstallActionReinstall,
		},
		{
			name: "upgrade",
			in:   "installed=1:0.1.0-1.x86_64\ncandidate=1:0.2.0-1.x86_64\ncmp=-1\n",
			want: InstallActionUpgrade,
		},
		{
			name: "downgrade",
			in:   "installed=1:0.2.0-1.x86_64\ncandidate=1:0.1.0-1.x86_64\ncmp=1\n",
			want: InstallActionDowngrade,
		},
		{
			name: "epoch-only diff still reinstall",
			in:   "installed=1:0.1.0-1.x86_64\ncandidate=2:0.1.0-1.x86_64\ncmp=0\n",
			want: InstallActionReinstall,
		},
		{
			name: "empty candidate is an error",
			in:   "installed=\ncandidate=\ncmp=0\n",
			err:  true,
		},
		{
			name: "unexpected cmp is an error",
			in:   "installed=a\ncandidate=b\ncmp=foo\n",
			err:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseInspectOutput(tc.in)
			if tc.err {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestDetectPackageManager(t *testing.T) {
	cases := []struct {
		name     string
		probeOut string
		probeErr error
		wantKind string
		wantErr  bool
	}{
		{name: "dnf wins over yum and apt", probeOut: "/usr/bin/dnf\n/usr/bin/yum\n/usr/bin/apt-get\n", wantKind: "rpm"},
		{name: "yum on EL7", probeOut: "\n/usr/bin/yum\n\n", wantKind: "rpm"},
		{name: "apt-get on debian", probeOut: "\n\n/usr/bin/apt-get\n", wantKind: "deb"},
		{name: "none → error", probeOut: "\n\n\n", wantErr: true},
		{name: "probe failure → error", probeErr: errors.New("ssh fail"), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runShell := func(_ context.Context, _ string) (string, error) {
				if tc.probeErr != nil {
					return "", tc.probeErr
				}
				return tc.probeOut, nil
			}
			mgr, err := DetectPackageManager(context.Background(), runShell)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got mgr=%v", mgr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if mgr.Kind() != tc.wantKind {
				t.Fatalf("kind: got %q want %q", mgr.Kind(), tc.wantKind)
			}
		})
	}
}
