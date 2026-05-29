package operations

import (
	"reflect"
	"testing"
)

func TestParseMountCheckOutput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "all mounted",
			in: "/mnt/data1/buckit\tmounted\n" +
				"/mnt/data2/buckit\tmounted\n",
			want: nil,
		},
		{
			name: "one not mounted",
			in: "/mnt/data1/buckit\tmounted\n" +
				"/mnt/data2/buckit\tnot-mounted:/mnt/data2\n",
			want: []string{"/mnt/data2/buckit: parent /mnt/data2 is not a mount point"},
		},
		{
			name: "single-drive rootfs passes (parent is /)",
			in:   "/buckit\tmounted\n",
			want: nil,
		},
		{
			name: "single-drive dedicated passes",
			in:   "/mnt/data1/buckit\tmounted\n",
			want: nil,
		},
		{
			name: "data path itself is the mount (no subdir layout) passes",
			in: "/mnt/data/drive0\tmounted\n" +
				"/mnt/data/drive1\tmounted\n" +
				"/mnt/data/drive2\tmounted\n" +
				"/mnt/data/drive3\tmounted\n",
			want: nil,
		},
		{
			name: "multiple not mounted",
			in: "/mnt/data1/buckit\tnot-mounted:/mnt/data1\n" +
				"/mnt/data2/buckit\tnot-mounted:/mnt/data2\n" +
				"/mnt/data3/buckit\tmounted\n",
			want: []string{
				"/mnt/data1/buckit: parent /mnt/data1 is not a mount point",
				"/mnt/data2/buckit: parent /mnt/data2 is not a mount point",
			},
		},
		{
			name: "blank + malformed lines are skipped",
			in:   "\n/mnt/data1/buckit\tmounted\nnotabline\n",
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseMountCheckOutput(tc.in)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
