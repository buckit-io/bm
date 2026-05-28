package operations

import "testing"

func TestExtractEnvVar(t *testing.T) {
	cases := []struct {
		name string
		body string
		key  string
		want string
	}{
		{
			name: "quoted",
			body: `MINIO_CONFIG_ENV_FILE="/etc/minio/config.env"` + "\n",
			key:  "MINIO_CONFIG_ENV_FILE",
			want: "/etc/minio/config.env",
		},
		{
			name: "single quoted",
			body: `MINIO_CONFIG_ENV_FILE='/etc/buckit/secrets'` + "\n",
			key:  "MINIO_CONFIG_ENV_FILE",
			want: "/etc/buckit/secrets",
		},
		{
			name: "unquoted",
			body: "MINIO_CONFIG_ENV_FILE=/etc/minio/config.env\n",
			key:  "MINIO_CONFIG_ENV_FILE",
			want: "/etc/minio/config.env",
		},
		{
			name: "export prefix",
			body: `export MINIO_OPTS="--address :9000"` + "\n",
			key:  "MINIO_OPTS",
			want: "--address :9000",
		},
		{
			name: "later assignment wins",
			body: "MINIO_REGION=us-west-2\nMINIO_REGION=us-east-1\n",
			key:  "MINIO_REGION",
			want: "us-east-1",
		},
		{
			name: "absent",
			body: "MINIO_VOLUMES=/mnt/data\n",
			key:  "MINIO_CONFIG_ENV_FILE",
			want: "",
		},
		{
			name: "inline comment on unquoted value",
			body: "MINIO_REGION=us-east-1 # primary\n",
			key:  "MINIO_REGION",
			want: "us-east-1",
		},
		{
			name: "hash inside quoted value is kept",
			body: `MINIO_OPTS="--address :9000 # do not change"` + "\n",
			key:  "MINIO_OPTS",
			want: "--address :9000 # do not change",
		},
		{
			name: "prefix match must be exact (MINIO_OPTS vs MINIO_OPTS_FOO)",
			body: "MINIO_OPTS_FOO=bar\n",
			key:  "MINIO_OPTS",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractEnvVar(tc.body, tc.key); got != tc.want {
				t.Fatalf("extractEnvVar(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

func TestExtractCertsDirFromOpts(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "space form",
			body: `MINIO_OPTS="--address :9000 --certs-dir /etc/minio/certs"` + "\n",
			want: "/etc/minio/certs",
		},
		{
			name: "equals form",
			body: `MINIO_OPTS="--address :9000 --certs-dir=/etc/buckit/tls"` + "\n",
			want: "/etc/buckit/tls",
		},
		{
			name: "no certs-dir flag",
			body: `MINIO_OPTS="--address :9000"` + "\n",
			want: "",
		},
		{
			name: "no MINIO_OPTS",
			body: "MINIO_VOLUMES=/mnt/data\n",
			want: "",
		},
		{
			name: "certs-dir at end with no value falls through",
			body: `MINIO_OPTS="--certs-dir"` + "\n",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractCertsDirFromOpts(tc.body); got != tc.want {
				t.Fatalf("extractCertsDirFromOpts = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPathBase(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/etc/minio/certs", "certs"},
		{"/etc/minio/certs/", "certs"},
		{"/certs", "certs"},
		{"certs", "certs"},
		{"/", ""},
	}
	for _, tc := range cases {
		if got := pathBase(tc.in); got != tc.want {
			t.Fatalf("pathBase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
