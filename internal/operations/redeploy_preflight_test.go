package operations

import (
	"reflect"
	"testing"
)

func TestLocalVolumePathsFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want []string
	}{
		{
			name: "distributed http pattern",
			env:  `MINIO_VOLUMES="https://h{1...4}:9000/mnt/data{1...4}/buckit"` + "\n",
			want: []string{
				"/mnt/data1/buckit",
				"/mnt/data2/buckit",
				"/mnt/data3/buckit",
				"/mnt/data4/buckit",
			},
		},
		{
			name: "bare paths",
			env:  `MINIO_VOLUMES="/mnt/data1 /mnt/data2"` + "\n",
			want: []string{"/mnt/data1", "/mnt/data2"},
		},
		{
			name: "single-node ellipses on bare path",
			env:  `MINIO_VOLUMES="/mnt/data{1...4}/buckit"` + "\n",
			want: []string{
				"/mnt/data1/buckit",
				"/mnt/data2/buckit",
				"/mnt/data3/buckit",
				"/mnt/data4/buckit",
			},
		},
		{
			name: "no MINIO_VOLUMES",
			env:  "MINIO_REGION=us-east-1\n",
			want: []string{},
		},
		{
			name: "zero-padded path ellipses",
			env:  `MINIO_VOLUMES="http://h1:9000/mnt/data{01...02}/buckit"` + "\n",
			want: []string{"/mnt/data01/buckit", "/mnt/data02/buckit"},
		},
		{
			name: "two separate args dedupe local paths",
			env:  `MINIO_VOLUMES="https://h{1...4}:9000/mnt/data{1...2}/buckit https://h{5...8}:9000/mnt/data{1...2}/buckit"` + "\n",
			want: []string{"/mnt/data1/buckit", "/mnt/data2/buckit"},
		},
		{
			name: "malformed pattern survives as literal",
			env:  `MINIO_VOLUMES="/mnt/data{bogus}/buckit"` + "\n",
			want: []string{"/mnt/data{bogus}/buckit"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := localVolumePathsFromEnv(tc.env)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParsePathStateOutput(t *testing.T) {
	out := "/etc/default/minio\tabsent\n" +
		"/etc/minio/config.env\tempty\n" +
		"/mnt/data1/buckit\tnonempty:.minio.sys\n" +
		"\n" + // blank
		"junk-without-tab\n"
	got := parsePathStateOutput(out)
	if got["/etc/default/minio"].kind != pathAbsent {
		t.Fatalf("expected absent, got %v", got["/etc/default/minio"])
	}
	if got["/etc/minio/config.env"].kind != pathEmpty {
		t.Fatalf("expected empty, got %v", got["/etc/minio/config.env"])
	}
	if got["/mnt/data1/buckit"].kind != pathNonEmpty || got["/mnt/data1/buckit"].detail != "contains .minio.sys" {
		t.Fatalf("expected nonempty with detail, got %v", got["/mnt/data1/buckit"])
	}
	if _, ok := got["junk-without-tab"]; ok {
		t.Fatalf("junk line should be dropped")
	}
}
