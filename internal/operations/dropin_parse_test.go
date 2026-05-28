package operations

import (
	"reflect"
	"testing"
)

func TestParseDropInPaths(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "single operator override",
			in:   "DropInPaths=/etc/systemd/system/buckit.service.d/10-override.conf\n",
			want: []string{"/etc/systemd/system/buckit.service.d/10-override.conf"},
		},
		{
			name: "multiple drop-ins in /etc",
			in:   "DropInPaths=/etc/systemd/system/buckit.service.d/10-restart.conf /etc/systemd/system/buckit.service.d/20-env.conf\n",
			want: []string{
				"/etc/systemd/system/buckit.service.d/10-restart.conf",
				"/etc/systemd/system/buckit.service.d/20-env.conf",
			},
		},
		{
			name: "package-shipped drop-ins under /usr are filtered out",
			in:   "DropInPaths=/usr/lib/systemd/system/buckit.service.d/00-pkg.conf /etc/systemd/system/buckit.service.d/10-override.conf\n",
			want: []string{"/etc/systemd/system/buckit.service.d/10-override.conf"},
		},
		{
			name: "transient /run drop-ins are filtered out",
			in:   "DropInPaths=/run/systemd/system/buckit.service.d/snap.conf /etc/systemd/system/buckit.service.d/10-override.conf\n",
			want: []string{"/etc/systemd/system/buckit.service.d/10-override.conf"},
		},
		{
			name: "empty DropInPaths",
			in:   "DropInPaths=\n",
			want: nil,
		},
		{
			name: "no DropInPaths line",
			in:   "User=buckit\nGroup=buckit\n",
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseDropInPaths(tc.in)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
