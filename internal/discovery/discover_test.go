package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	madmin "github.com/buckit-io/madmin-go/v3"

	"github.com/buckit-io/bm/internal/domain"
)

func fakeAdminServer(t *testing.T) *httptest.Server {
	t.Helper()
	infoBody := madmin.InfoMessage{
		Mode: "online",
		Servers: []madmin.ServerProperties{
			{
				Endpoint:   "node1:9000",
				State:      "online",
				Version:    "RELEASE.2026-05-15T10-00-00Z", // post-cutoff -> Buckit
				PoolNumber: 1,
				Uptime:     120,
				Disks: []madmin.Disk{
					{Endpoint: "/dev/sda", DrivePath: "/", State: "ok", TotalSpace: 256 * 1024 * 1024 * 1024, UsedSpace: 50 * 1024 * 1024 * 1024, RootDisk: true},
					{Endpoint: "/dev/sdb", DrivePath: "/data/disk1", State: "ok", TotalSpace: 16 * 1024 * 1024 * 1024 * 1024, UsedSpace: 1 * 1024 * 1024 * 1024 * 1024},
					{Endpoint: "/dev/sdc", DrivePath: "/data/disk2", State: "ok", TotalSpace: 16 * 1024 * 1024 * 1024 * 1024, UsedSpace: 1 * 1024 * 1024 * 1024 * 1024},
				},
			},
			{
				Endpoint:   "node2:9000",
				State:      "online",
				Version:    "RELEASE.2026-05-15T10-00-00Z",
				PoolNumber: 1,
				Disks: []madmin.Disk{
					{Endpoint: "/dev/sdb", DrivePath: "/data/disk1", State: "ok", TotalSpace: 16 * 1024 * 1024 * 1024 * 1024, UsedSpace: 1 * 1024 * 1024 * 1024 * 1024},
				},
			},
		},
		Backend: madmin.ErasureBackend{Type: "Erasure", StandardSCParity: 2},
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Crude path routing: madmin sends GET /minio/admin/v3/info?<opts>.
		if !strings.Contains(r.URL.Path, "/admin/v3/info") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(infoBody)
	}))
}

func collectProgress(ch <-chan domain.DiscoveryProgress) []domain.DiscoveryProgress {
	var out []domain.DiscoveryProgress
	for line := range ch {
		out = append(out, line)
	}
	return out
}

func TestDiscoverSuccess(t *testing.T) {
	srv := fakeAdminServer(t)
	defer srv.Close()

	progressCh := make(chan domain.DiscoveryProgress, 32)
	done := make(chan struct{})
	var lines []domain.DiscoveryProgress
	go func() {
		lines = collectProgress(progressCh)
		close(done)
	}()

	candidate, err := Discover(context.Background(), Request{
		URL:      srv.URL,
		Username: "ak",
		Password: "sk",
		Insecure: true,
	}, progressCh)
	close(progressCh)
	<-done

	if err != nil {
		t.Fatalf("discover: %v (lines=%v)", err, lines)
	}
	if candidate == nil {
		t.Fatal("nil candidate")
	}
	if candidate.Engine != domain.EngineBuckit {
		t.Fatalf("want Buckit (post-cutoff version), got %s", candidate.Engine)
	}
	if len(candidate.Nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d", len(candidate.Nodes))
	}
	if candidate.PoolCount != 1 {
		t.Fatalf("want 1 pool, got %d", candidate.PoolCount)
	}
	if candidate.DriveCount != 3 {
		t.Fatalf("want 3 data drives, got %d", candidate.DriveCount)
	}
	if candidate.Parity != 2 {
		t.Fatalf("want parity 2, got %d", candidate.Parity)
	}
	if len(lines) < 5 {
		t.Fatalf("expected several progress lines, got %d", len(lines))
	}
}

func TestDiscoverInvalidURL(t *testing.T) {
	progressCh := make(chan domain.DiscoveryProgress, 8)
	go func() {
		for range progressCh {
		}
	}()
	defer close(progressCh)

	_, err := Discover(context.Background(), Request{URL: "", Username: "u", Password: "p"}, progressCh)
	var ie *ImportError
	if !errors.As(err, &ie) || ie.Inner.Kind != domain.ImportErrInvalidURL {
		t.Fatalf("want InvalidURL ImportError, got %v", err)
	}
}

func TestDiscoverAuthMissing(t *testing.T) {
	progressCh := make(chan domain.DiscoveryProgress, 8)
	go func() {
		for range progressCh {
		}
	}()
	defer close(progressCh)

	_, err := Discover(context.Background(), Request{URL: "https://example", Username: "", Password: ""}, progressCh)
	var ie *ImportError
	if !errors.As(err, &ie) || ie.Inner.Kind != domain.ImportErrAuth {
		t.Fatalf("want Auth ImportError, got %v", err)
	}
}

func TestDiscoverUnreachable(t *testing.T) {
	progressCh := make(chan domain.DiscoveryProgress, 32)
	done := make(chan struct{})
	go func() {
		for range progressCh {
		}
		close(done)
	}()

	// Bind to an arbitrary closed port (127.0.0.1:1) — connection refused.
	_, err := Discover(context.Background(), Request{
		URL:      "http://127.0.0.1:1",
		Username: "ak",
		Password: "sk",
	}, progressCh)
	close(progressCh)
	<-done

	var ie *ImportError
	if !errors.As(err, &ie) {
		t.Fatalf("want ImportError, got %v", err)
	}
	if ie.Inner.Kind != domain.ImportErrUnreachable {
		t.Fatalf("want Unreachable, got %s", ie.Inner.Kind)
	}
}
