package migration

import (
	"context"
	"strings"
	"testing"

	"github.com/buckit-io/bm/internal/domain"
)

// fakeConn satisfies preflight.HostConn. Routes commands through a
// per-host map keyed by the command string; canned response is returned
// verbatim. HEAD is unused by the checks under test here.
type fakeConn struct {
	commands map[string]cannedResp
}

type cannedResp struct {
	stdout string
	stderr string
	exit   int
	err    error
}

func (f *fakeConn) Run(_ context.Context, _ domain.HostRow, cmd string) (string, string, int, error) {
	if r, ok := f.commands[cmd]; ok {
		return r.stdout, r.stderr, r.exit, r.err
	}
	return "", "", 0, nil
}

func (f *fakeConn) HEAD(_ context.Context, _ string) (int, int64, error) {
	return 200, 0, nil
}

func draftWithHosts(names ...string) domain.NewClusterDraft {
	hosts := make([]domain.HostRow, 0, len(names))
	for i, n := range names {
		hosts = append(hosts, domain.HostRow{
			ID:       n,
			Hostname: n,
			Port:     22,
			Probe:    domain.HostProbeReachable,
		})
		_ = i
	}
	return domain.NewClusterDraft{Hosts: hosts}
}

func TestMinioServicePresent(t *testing.T) {
	t.Run("unit registered on every host → pass", func(t *testing.T) {
		conn := &fakeConn{commands: map[string]cannedResp{
			"systemctl cat minio.service --no-pager": {stdout: "# /lib/systemd/system/minio.service\n[Unit]\nDescription=MinIO\n", exit: 0},
		}}
		out := minioServicePresent(context.Background(), conn, draftWithHosts("h1", "h2"))
		for _, hs := range out.HostStatuses {
			if hs.Status != domain.PreflightPass {
				t.Fatalf("host %s: want pass, got %s (%q)", hs.HostID, hs.Status, hs.Message)
			}
		}
	})
	t.Run("unit missing → fail with stderr in detail", func(t *testing.T) {
		conn := &fakeConn{commands: map[string]cannedResp{
			"systemctl cat minio.service --no-pager": {stderr: "No files found for minio.service.", exit: 1},
		}}
		out := minioServicePresent(context.Background(), conn, draftWithHosts("h1"))
		if got := out.HostStatuses[0].Status; got != domain.PreflightFail {
			t.Fatalf("want fail, got %s", got)
		}
		if !strings.Contains(out.HostStatuses[0].Message, "No files found") {
			t.Fatalf("expected stderr in detail, got %q", out.HostStatuses[0].Message)
		}
	})
}

func TestMinioServiceComplete(t *testing.T) {
	const showCmd = "systemctl show minio.service -p User -p Group -p EnvironmentFiles -p EnvironmentFile"

	probeAllYes := "user=yes\ngroup=yes\nenv=yes\n"
	probeAllNo := "user=no\ngroup=no\nenv=no\n"

	t.Run("all three fields present → pass (no fallback probe needed)", func(t *testing.T) {
		conn := &fakeConn{commands: map[string]cannedResp{
			showCmd: {stdout: "User=minio-user\nGroup=minio-user\nEnvironmentFiles=/etc/default/minio (ignore_errors=yes)\n"},
		}}
		out := minioServiceComplete(context.Background(), conn, draftWithHosts("h1"))
		hs := out.HostStatuses[0]
		if hs.Status != domain.PreflightPass {
			t.Fatalf("want pass, got %s (%q)", hs.Status, hs.Message)
		}
		for _, want := range []string{"User=minio-user", "Group=minio-user", "EnvironmentFile=/etc/default/minio"} {
			if !strings.Contains(hs.Message, want) {
				t.Fatalf("want %q in detail, got %q", want, hs.Message)
			}
		}
	})

	warnCases := []struct {
		name     string
		stdout   string
		wantSubs []string
	}{
		{
			name:     "User missing but minio-user exists → warn",
			stdout:   "User=\nGroup=minio-user\nEnvironmentFiles=/etc/default/minio (ignore_errors=yes)\n",
			wantSubs: []string{"User→minio-user"},
		},
		{
			name:     "Group missing but minio-user group exists → warn",
			stdout:   "User=minio-user\nGroup=\nEnvironmentFiles=/etc/default/minio (ignore_errors=yes)\n",
			wantSubs: []string{"Group→minio-user"},
		},
		{
			name:     "EnvironmentFile missing but /etc/default/minio exists → warn",
			stdout:   "User=minio-user\nGroup=minio-user\nEnvironmentFile=\n",
			wantSubs: []string{"EnvironmentFile→/etc/default/minio"},
		},
		{
			name:     "all three missing but all fallbacks exist → warn listing all substitutions",
			stdout:   "User=\nGroup=\nEnvironmentFile=\n",
			wantSubs: []string{"User→minio-user", "Group→minio-user", "EnvironmentFile→/etc/default/minio"},
		},
	}
	for _, tc := range warnCases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &fakeConn{commands: map[string]cannedResp{
				showCmd:          {stdout: tc.stdout},
				fallbackProbeCmd: {stdout: probeAllYes},
			}}
			out := minioServiceComplete(context.Background(), conn, draftWithHosts("h1"))
			hs := out.HostStatuses[0]
			if hs.Status != domain.PreflightWarn {
				t.Fatalf("want warn, got %s (%q)", hs.Status, hs.Message)
			}
			for _, want := range tc.wantSubs {
				if !strings.Contains(hs.Message, want) {
					t.Fatalf("want %q in detail, got %q", want, hs.Message)
				}
			}
		})
	}

	failCases := []struct {
		name       string
		stdout     string
		probe      string
		wantInDetail []string
	}{
		{
			name:         "User missing AND minio-user user absent → fail",
			stdout:       "User=\nGroup=minio-user\nEnvironmentFiles=/etc/default/minio (ignore_errors=yes)\n",
			probe:        probeAllNo,
			wantInDetail: []string{"no fallback", "User"},
		},
		{
			name:         "EnvironmentFile missing AND /etc/default/minio absent → fail",
			stdout:       "User=minio-user\nGroup=minio-user\nEnvironmentFile=\n",
			probe:        probeAllNo,
			wantInDetail: []string{"no fallback", "EnvironmentFile"},
		},
		{
			name:         "partial fallback availability — User OK but Group missing → fail names only the unresolvable",
			stdout:       "User=\nGroup=\nEnvironmentFiles=/etc/default/minio (ignore_errors=yes)\n",
			probe:        "user=yes\ngroup=no\nenv=yes\n",
			wantInDetail: []string{"Group"},
		},
	}
	for _, tc := range failCases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &fakeConn{commands: map[string]cannedResp{
				showCmd:          {stdout: tc.stdout},
				fallbackProbeCmd: {stdout: tc.probe},
			}}
			out := minioServiceComplete(context.Background(), conn, draftWithHosts("h1"))
			hs := out.HostStatuses[0]
			if hs.Status != domain.PreflightFail {
				t.Fatalf("want fail, got %s (%q)", hs.Status, hs.Message)
			}
			for _, want := range tc.wantInDetail {
				if !strings.Contains(hs.Message, want) {
					t.Fatalf("want %q in detail, got %q", want, hs.Message)
				}
			}
		})
	}

	t.Run("non-zero exit from systemctl show → fail with stderr", func(t *testing.T) {
		conn := &fakeConn{commands: map[string]cannedResp{
			showCmd: {stderr: "Failed to get unit file state", exit: 1},
		}}
		out := minioServiceComplete(context.Background(), conn, draftWithHosts("h1"))
		hs := out.HostStatuses[0]
		if hs.Status != domain.PreflightFail {
			t.Fatalf("want fail, got %s (%q)", hs.Status, hs.Message)
		}
		if !strings.Contains(hs.Message, "Failed to get") {
			t.Fatalf("expected stderr in detail, got %q", hs.Message)
		}
	})
}

func TestParseFallbackProbe(t *testing.T) {
	cases := []struct {
		in   string
		want fallbackProbe
	}{
		{"user=yes\ngroup=yes\nenv=yes\n", fallbackProbe{true, true, true}},
		{"user=no\ngroup=no\nenv=no\n", fallbackProbe{false, false, false}},
		{"user=yes\ngroup=no\nenv=yes\n", fallbackProbe{true, false, true}},
		{"", fallbackProbe{}},
		// Whitespace tolerant.
		{"  user=yes  \n  group=yes  \n  env=yes  \n", fallbackProbe{true, true, true}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := parseFallbackProbe(tc.in)
			if got != tc.want {
				t.Fatalf("parseFallbackProbe(%q): want %#v, got %#v", tc.in, tc.want, got)
			}
		})
	}
}

func TestCatalogIncludesNewChecks(t *testing.T) {
	catalog := Catalog(domain.AdminCreds{URL: "http://x", AccessKey: "a", SecretKey: "b"})
	want := map[string]domain.PreflightSeverity{
		"minio_service_present":  domain.PreflightBlocking,
		"minio_service_complete": domain.PreflightBlocking,
	}
	got := map[string]domain.PreflightSeverity{}
	for _, c := range catalog {
		got[c.ID] = c.Severity
	}
	for id, sev := range want {
		if got[id] == "" {
			t.Errorf("catalog missing %q", id)
			continue
		}
		if got[id] != sev {
			t.Errorf("%q: want severity %s, got %s", id, sev, got[id])
		}
	}
}
