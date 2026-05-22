package deploy

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/buckit-io/bm/internal/domain"
)

func TestFetchRPMChecksumSingleEntry(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  buckit.rpm\n"))
	}))
	srv.Listener = ln
	srv.Start()
	defer srv.Close()

	sum, err := FetchRPMChecksum(context.Background(), RPMArtifact{
		URL:        "https://example.test/buckit.rpm",
		SHA256URLs: []string{srv.URL},
	})
	if err != nil {
		t.Fatalf("FetchRPMChecksum: %v", err)
	}
	if sum != strings.Repeat("a", 64) {
		t.Fatalf("unexpected sum: %q", sum)
	}
}

func TestFetchRPMChecksumMatchesArtifactName(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Join([]string{
			"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  buckit-amd64.rpm",
			"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc  buckit-arm64.rpm",
		}, "\n")))
	}))
	srv.Listener = ln
	srv.Start()
	defer srv.Close()

	sum, err := FetchRPMChecksum(context.Background(), RPMArtifact{
		URL:        "https://example.test/buckit-arm64.rpm",
		SHA256URLs: []string{srv.URL},
	})
	if err != nil {
		t.Fatalf("FetchRPMChecksum: %v", err)
	}
	if sum != strings.Repeat("c", 64) {
		t.Fatalf("unexpected sum: %q", sum)
	}
}

func TestResolveRPMArtifactFallsBackToSiblingSHA256URL(t *testing.T) {
	restore := RestoreVersionsCacheForTest([]domain.BuckitVersion{{
		Tag:    "v1.0.0",
		Label:  "v1.0.0",
		RpmURL: "https://example.test/buckit.rpm",
	}})
	defer restore()

	artifact, err := ResolveRPMArtifact("v1.0.0", "")
	if err != nil {
		t.Fatalf("ResolveRPMArtifact: %v", err)
	}
	if len(artifact.SHA256URLs) != 2 {
		t.Fatalf("unexpected sha256 URLs: %+v", artifact.SHA256URLs)
	}
	if artifact.SHA256URLs[0] != "https://example.test/buckit.rpm.sha256sum" {
		t.Fatalf("unexpected first sha256 URL: %q", artifact.SHA256URLs[0])
	}
	if artifact.SHA256URLs[1] != "https://example.test/buckit.rpm.sha256" {
		t.Fatalf("unexpected second sha256 URL: %q", artifact.SHA256URLs[1])
	}
}

func TestResolveRPMArtifactUsesArchSpecificSHA256URL(t *testing.T) {
	restore := RestoreVersionsCacheForTest([]domain.BuckitVersion{{
		Tag:            "v1.0.0",
		Label:          "v1.0.0",
		RpmURL:         "https://example.test/buckit-amd64.rpm",
		RpmURLAmd64:    "https://example.test/buckit-amd64.rpm",
		RpmURLArm64:    "https://example.test/buckit-arm64.rpm",
		SHA256URL:      "https://example.test/generic.sha256sum",
		SHA256URLAmd64: "https://example.test/buckit-amd64.rpm.sha256sum",
		SHA256URLArm64: "https://example.test/buckit-arm64.rpm.sha256sum",
	}})
	defer restore()

	artifact, err := ResolveRPMArtifact("v1.0.0", "arm64")
	if err != nil {
		t.Fatalf("ResolveRPMArtifact: %v", err)
	}
	if len(artifact.SHA256URLs) == 0 || artifact.SHA256URLs[0] != "https://example.test/buckit-arm64.rpm.sha256sum" {
		t.Fatalf("unexpected sha256 URL order: %+v", artifact.SHA256URLs)
	}
}

func TestResolveRPMArtifactUsesPerArtifactCatalogEntry(t *testing.T) {
	restore := RestoreVersionsCacheForTest([]domain.BuckitVersion{{
		Tag:   "v1.0.0",
		Label: "v1.0.0",
		Artifacts: []domain.BuckitArtifact{
			{Kind: "rpm", OS: "linux", Arch: "amd64", URL: "https://example.test/buckit-amd64.rpm", SHA256URL: "https://example.test/buckit-amd64.rpm.sha256sum"},
			{Kind: "deb", OS: "linux", Arch: "arm64", URL: "https://example.test/buckit-arm64.deb", SHA256URL: "https://example.test/buckit-arm64.deb.sha256sum"},
			{Kind: "rpm", OS: "linux", Arch: "arm64", URL: "https://example.test/buckit-arm64.rpm", SHA256URL: "https://example.test/buckit-arm64.rpm.sha256sum"},
		},
	}})
	defer restore()

	artifact, err := ResolveRPMArtifact("v1.0.0", "arm64")
	if err != nil {
		t.Fatalf("ResolveRPMArtifact: %v", err)
	}
	if artifact.URL != "https://example.test/buckit-arm64.rpm" {
		t.Fatalf("unexpected rpm URL: %q", artifact.URL)
	}
	if len(artifact.SHA256URLs) == 0 || artifact.SHA256URLs[0] != "https://example.test/buckit-arm64.rpm.sha256sum" {
		t.Fatalf("unexpected sha256 URL order: %+v", artifact.SHA256URLs)
	}
}

func TestFetchRPMChecksumTriesSha256sumThenSha256ThenDigest(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var hits []string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		switch r.URL.Path {
		case "/pkg.rpm.sha256sum":
			http.NotFound(w, r)
		case "/pkg.rpm.sha256":
			_, _ = w.Write([]byte("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd  pkg.rpm\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	srv.Listener = ln
	srv.Start()
	defer srv.Close()

	sum, err := FetchRPMChecksum(context.Background(), RPMArtifact{
		URL:        "https://example.test/pkg.rpm",
		SHA256URLs: []string{srv.URL + "/pkg.rpm.sha256sum", srv.URL + "/pkg.rpm.sha256"},
		SHA256:     strings.Repeat("e", 64),
	})
	if err != nil {
		t.Fatalf("FetchRPMChecksum: %v", err)
	}
	if sum != strings.Repeat("d", 64) {
		t.Fatalf("unexpected sum: %q", sum)
	}
	if strings.Join(hits, ",") != "/pkg.rpm.sha256sum,/pkg.rpm.sha256" {
		t.Fatalf("unexpected fetch order: %+v", hits)
	}
}

func TestFetchRPMChecksumFallsBackToGitHubDigest(t *testing.T) {
	sum, err := FetchRPMChecksum(context.Background(), RPMArtifact{
		URL:        "https://example.test/pkg.rpm",
		SHA256URLs: []string{"https://example.test/pkg.rpm.sha256sum", "https://example.test/pkg.rpm.sha256"},
		SHA256:     strings.Repeat("f", 64),
	})
	if err != nil {
		t.Fatalf("FetchRPMChecksum: %v", err)
	}
	if sum != strings.Repeat("f", 64) {
		t.Fatalf("unexpected sum: %q", sum)
	}
}
