package api

import "testing"

func TestAssertLoopback(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		addr    string
		wantErr bool
	}{
		{name: "empty host", addr: ":9443"},
		{name: "ipv4 loopback", addr: "127.0.0.1:9443"},
		{name: "ipv6 loopback", addr: "[::1]:9443"},
		{name: "localhost", addr: "localhost:9443"},
		{name: "wildcard", addr: "0.0.0.0:9443", wantErr: true},
		{name: "host network", addr: "192.168.1.10:9443", wantErr: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := AssertLoopback(tc.addr)
			if tc.wantErr && err == nil {
				t.Fatalf("AssertLoopback(%q): want error, got nil", tc.addr)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("AssertLoopback(%q): want nil, got %v", tc.addr, err)
			}
		})
	}
}
