package migration

import (
	"strings"
	"testing"

	"github.com/buckit-io/bm/internal/deploy"
)

func TestRenderDropIn(t *testing.T) {
	cases := []struct {
		name    string
		old     deploy.UnitProps
		new     deploy.UnitProps
		wantHas []string // substrings that must appear in the body
		wantOut bool     // true → expect non-empty body; false → expect ""
	}{
		{
			name: "fully aligned — no drop-in",
			old:  deploy.UnitProps{User: "minio-user", Group: "minio-user", EnvironmentFiles: []string{"/etc/default/minio"}},
			new:  deploy.UnitProps{User: "minio-user", Group: "minio-user", EnvironmentFiles: []string{"/etc/default/minio"}},
		},
		{
			name:    "User differs — override only User",
			old:     deploy.UnitProps{User: "minio-user", Group: "minio-user", EnvironmentFiles: []string{"/etc/default/minio"}},
			new:     deploy.UnitProps{User: "buckit", Group: "minio-user", EnvironmentFiles: []string{"/etc/default/minio"}},
			wantHas: []string{"[Service]", "User=minio-user"},
			wantOut: true,
		},
		{
			name:    "User and Group differ — override both",
			old:     deploy.UnitProps{User: "minio-user", Group: "minio-user", EnvironmentFiles: []string{"/etc/default/minio"}},
			new:     deploy.UnitProps{User: "buckit", Group: "buckit", EnvironmentFiles: []string{"/etc/default/minio"}},
			wantHas: []string{"User=minio-user", "Group=minio-user"},
			wantOut: true,
		},
		{
			name:    "EnvironmentFile differs — emit reset + new entry",
			old:     deploy.UnitProps{User: "minio-user", Group: "minio-user", EnvironmentFiles: []string{"/etc/default/minio"}},
			new:     deploy.UnitProps{User: "minio-user", Group: "minio-user", EnvironmentFiles: []string{"/etc/default/buckit"}},
			wantHas: []string{"EnvironmentFile=\n", "EnvironmentFile=-/etc/default/minio"},
			wantOut: true,
		},
		{
			name:    "multiple EnvironmentFile paths preserved in order",
			old:     deploy.UnitProps{User: "minio-user", Group: "minio-user", EnvironmentFiles: []string{"/etc/default/minio", "/etc/default/minio.local"}},
			new:     deploy.UnitProps{User: "minio-user", Group: "minio-user", EnvironmentFiles: []string{"/etc/default/buckit"}},
			wantHas: []string{"EnvironmentFile=\nEnvironmentFile=-/etc/default/minio\nEnvironmentFile=-/etc/default/minio.local"},
			wantOut: true,
		},
		{
			// Asymmetric defaults: OLD missing → conventional MinIO
			// fallback (minio-user); NEW missing → systemd's root default.
			name:    "old User/Group missing → drop-in writes minio-user (fallback) over root (buckit default)",
			old:     deploy.UnitProps{EnvironmentFiles: []string{"/etc/default/minio"}},
			new:     deploy.UnitProps{EnvironmentFiles: []string{"/etc/default/minio"}},
			wantHas: []string{"User=minio-user", "Group=minio-user"},
			wantOut: true,
		},
		{
			// Old EnvironmentFile missing → uses conventional fallback.
			// New EnvironmentFile already matches → no env-file lines.
			name:    "old EnvironmentFile missing falls back to /etc/default/minio",
			old:     deploy.UnitProps{User: "minio-user", Group: "minio-user"},
			new:     deploy.UnitProps{User: "minio-user", Group: "minio-user", EnvironmentFiles: []string{"/etc/default/minio"}},
			wantHas: nil, // body should be empty — fallbacks align with new side
		},
		{
			// Both sides empty — minio-user (fallback) vs root (systemd default)
			// → drop-in needed; env-file fallback (/etc/default/minio) ≠ new's empty
			// list → env-file lines also emitted.
			name:    "both unset — fallbacks on old vs systemd defaults on new force a drop-in",
			old:     deploy.UnitProps{},
			new:     deploy.UnitProps{},
			wantHas: []string{"User=minio-user", "Group=minio-user", "EnvironmentFile=-/etc/default/minio"},
			wantOut: true,
		},
		{
			name: "everything differs",
			old:  deploy.UnitProps{User: "minio-user", Group: "minio-user", EnvironmentFiles: []string{"/etc/sysconfig/minio"}},
			new:  deploy.UnitProps{User: "buckit", Group: "buckit", EnvironmentFiles: []string{"/etc/default/buckit"}},
			wantHas: []string{
				"User=minio-user",
				"Group=minio-user",
				"EnvironmentFile=\n",
				"EnvironmentFile=-/etc/sysconfig/minio",
			},
			wantOut: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderDropIn(tc.old, tc.new)
			if tc.wantOut && got == "" {
				t.Fatalf("expected non-empty drop-in body, got empty")
			}
			if !tc.wantOut && got != "" {
				t.Fatalf("expected empty drop-in body, got:\n%s", got)
			}
			for _, want := range tc.wantHas {
				if !strings.Contains(got, want) {
					t.Fatalf("body missing %q:\n%s", want, got)
				}
			}
		})
	}
}

func TestWriteAndRemoveDropInCmds(t *testing.T) {
	t.Run("writeDropInCmd creates dir, writes body, sets mode", func(t *testing.T) {
		body := "[Service]\nUser=minio-user\n"
		got := writeDropInCmd(body)
		for _, want := range []string{
			"mkdir -p " + dropInDir,
			"tee " + dropInPath,
			"<<'BMDROPIN'",
			"User=minio-user",
			"BMDROPIN",
			"chmod 644 " + dropInPath,
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("writeDropInCmd missing %q:\n%s", want, got)
			}
		}
	})
	t.Run("heredoc terminator must be on a line by itself", func(t *testing.T) {
		// Regression: an earlier version joined the steps with " && "
		// instead of "\n", which put the terminator on the same line
		// as the chmod command. bash then never recognized the end of
		// the heredoc, and the literal text "BMDROPIN && chmod ..."
		// got dumped into the drop-in file — systemd then refused the
		// stray line as "Missing '='".
		got := writeDropInCmd("[Service]\nUser=minio-user\n")
		if !strings.Contains(got, "\nBMDROPIN\n") {
			t.Fatalf("BMDROPIN terminator not isolated on its own line:\n%s", got)
		}
		if strings.Contains(got, "BMDROPIN &&") || strings.Contains(got, "BMDROPIN ") {
			t.Fatalf("BMDROPIN must not be followed by content on the same line:\n%s", got)
		}
	})
	t.Run("removeDropInCmd removes file + best-effort rmdir", func(t *testing.T) {
		got := removeDropInCmd()
		for _, want := range []string{
			"rm -f " + dropInPath,
			"rmdir " + dropInDir,
			"|| true",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("removeDropInCmd missing %q:\n%s", want, got)
			}
		}
	})
}
