package ssh

import (
	"context"
	"strings"
	"testing"

	"github.com/buckit-io/bm/internal/domain"
)

func TestProbeAgainstTestServer(t *testing.T) {
	srv := newTestServer(t)
	host, port := srv.HostPort()
	res := Probe(context.Background(), domain.HostRef{ID: "h1", Hostname: host, Port: port}, Resolved{
		AuthMethod: domain.AuthPassword,
		User:       srv.user,
		Password:   srv.password,
	})
	if !res.OK {
		t.Fatalf("probe failed: %+v", res)
	}
	if !strings.Contains(res.Kernel, "Linux") {
		t.Fatalf("missing kernel: %q", res.Kernel)
	}
	if res.Hostname != "fakehost" {
		t.Fatalf("missing hostname: %q", res.Hostname)
	}
	if !strings.Contains(res.OS, "bm-test") {
		t.Fatalf("missing os: %q", res.OS)
	}
}

func TestProbeBadAuth(t *testing.T) {
	srv := newTestServer(t)
	host, port := srv.HostPort()
	res := Probe(context.Background(), domain.HostRef{Hostname: host, Port: port}, Resolved{
		AuthMethod: domain.AuthPassword,
		User:       "wrong",
		Password:   "wrong",
	})
	if res.OK {
		t.Fatal("expected probe failure for bad auth")
	}
	if res.Error == "" {
		t.Fatal("expected non-empty error")
	}
}
