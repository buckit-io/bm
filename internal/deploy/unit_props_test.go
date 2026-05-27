package deploy

import (
	"reflect"
	"testing"
)

func TestParseUnitProps(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want UnitProps
	}{
		{
			name: "minio.service typical (Debian/Ubuntu)",
			in: "User=minio-user\n" +
				"Group=minio-user\n" +
				"EnvironmentFile=/etc/default/minio (ignore_errors=yes)\n",
			want: UnitProps{
				User:             "minio-user",
				Group:            "minio-user",
				EnvironmentFiles: []string{"/etc/default/minio"},
			},
		},
		{
			name: "modern systemd plural property name",
			in: "User=minio-user\n" +
				"Group=minio-user\n" +
				"EnvironmentFiles=/etc/default/minio (ignore_errors=yes)\n",
			want: UnitProps{
				User:             "minio-user",
				Group:            "minio-user",
				EnvironmentFiles: []string{"/etc/default/minio"},
			},
		},
		{
			name: "minio.service typical (RHEL/Fedora)",
			in: "User=minio-user\n" +
				"Group=minio\n" +
				"EnvironmentFile=/etc/sysconfig/minio (ignore_errors=no)\n",
			want: UnitProps{
				User:             "minio-user",
				Group:            "minio",
				EnvironmentFiles: []string{"/etc/sysconfig/minio"},
			},
		},
		{
			name: "buckit.service shipping defaults",
			in: "User=buckit\n" +
				"Group=buckit\n" +
				"EnvironmentFile=/etc/default/minio (ignore_errors=yes)\n",
			want: UnitProps{
				User:             "buckit",
				Group:            "buckit",
				EnvironmentFiles: []string{"/etc/default/minio"},
			},
		},
		{
			name: "multiple EnvironmentFile entries — order preserved",
			in: "User=minio-user\n" +
				"Group=minio-user\n" +
				"EnvironmentFile=/etc/default/minio (ignore_errors=yes)\n" +
				"EnvironmentFile=/etc/default/minio.local (ignore_errors=yes)\n",
			want: UnitProps{
				User:             "minio-user",
				Group:            "minio-user",
				EnvironmentFiles: []string{"/etc/default/minio", "/etc/default/minio.local"},
			},
		},
		{
			name: "no EnvironmentFile set",
			in: "User=minio-user\n" +
				"Group=minio-user\n" +
				"EnvironmentFile=\n",
			want: UnitProps{
				User:  "minio-user",
				Group: "minio-user",
			},
		},
		{
			name: "leading dash ignore-errors marker echoed back",
			in: "User=minio-user\n" +
				"Group=minio-user\n" +
				"EnvironmentFile=-/etc/default/minio\n",
			want: UnitProps{
				User:             "minio-user",
				Group:            "minio-user",
				EnvironmentFiles: []string{"/etc/default/minio"},
			},
		},
		{
			name: "User/Group missing — systemd defaults are caller's concern",
			in:   "User=\nGroup=\nEnvironmentFile=/etc/default/minio (ignore_errors=yes)\n",
			want: UnitProps{
				EnvironmentFiles: []string{"/etc/default/minio"},
			},
		},
		{
			name: "interleaved + whitespace tolerant",
			in: "  EnvironmentFile=/etc/default/minio (ignore_errors=yes)  \n" +
				"User=minio-user\n" +
				"Group=minio-user\n",
			want: UnitProps{
				User:             "minio-user",
				Group:            "minio-user",
				EnvironmentFiles: []string{"/etc/default/minio"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseUnitProps(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseUnitProps:\n got: %#v\nwant: %#v", got, tc.want)
			}
		})
	}
}

func TestParseEnvironmentFileValue(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/etc/default/minio (ignore_errors=yes)", "/etc/default/minio"},
		{"/etc/default/minio (ignore_errors=no)", "/etc/default/minio"},
		{"/etc/default/minio", "/etc/default/minio"},
		{"-/etc/default/minio", "/etc/default/minio"},
		{"-/etc/default/minio (ignore_errors=yes)", "/etc/default/minio"},
		{"  /etc/default/minio  ", "/etc/default/minio"},
		{"", ""},
		{"  ", ""},
		// Paranoid: path with spaces. Not idiomatic for env files but the
		// parser shouldn't drop characters before the last `(`-annotation.
		{"/etc/has space/minio (ignore_errors=yes)", "/etc/has space/minio"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := parseEnvironmentFileValue(tc.in); got != tc.want {
				t.Fatalf("parseEnvironmentFileValue(%q): want %q, got %q", tc.in, tc.want, got)
			}
		})
	}
}
