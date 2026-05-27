package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParsePointer(t *testing.T) {
	sum := sha256.Sum256([]byte("bm"))
	gotSum, gotTag, err := parsePointer(hex.EncodeToString(sum[:]) + "  bm.RELEASE.2026-05-27T22-15-00Z")
	if err != nil {
		t.Fatalf("parsePointer() error = %v", err)
	}
	if gotTag != "RELEASE.2026-05-27T22-15-00Z" {
		t.Fatalf("tag = %q", gotTag)
	}
	if hex.EncodeToString(gotSum) != hex.EncodeToString(sum[:]) {
		t.Fatalf("sum mismatch")
	}
}

func TestCheckReportsRestartRequiredAfterApply(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "bm")
	oldBytes := []byte("old RELEASE.2026-05-01T00-00-00Z binary")
	if err := os.WriteFile(target, oldBytes, 0o755); err != nil {
		t.Fatal(err)
	}

	newBytes := []byte("new RELEASE.2026-06-01T00-00-00Z binary")
	sum := sha256.Sum256(newBytes)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/" + platform() + "/bm.sha256sum":
			return textResponse(hex.EncodeToString(sum[:]) + "  bm.RELEASE.2026-06-01T00-00-00Z"), nil
		case "/RELEASE.2026-06-01T00-00-00Z/bm-" + platform() + binaryExt() + ".RELEASE.2026-06-01T00-00-00Z":
			return bytesResponse(newBytes), nil
		default:
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Status:     "404 Not Found",
				Body:       io.NopCloser(strings.NewReader("not found")),
				Header:     make(http.Header),
			}, nil
		}
	})}

	svc := &Service{
		Client:                       client,
		PointerBaseURL:               "https://updates.test",
		ReleasesBaseURL:              "https://updates.test",
		CurrentVersion:               "RELEASE.2026-05-01T00-00-00Z",
		TargetPath:                   target,
		DisableSignatureVerification: true,
	}

	before, err := svc.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() before apply error = %v", err)
	}
	if !before.UpdateAvailable {
		t.Fatalf("expected update available before apply")
	}

	result, err := svc.Apply(context.Background())
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !result.Applied {
		t.Fatalf("expected applied result")
	}
	if result.InstalledVersion != "RELEASE.2026-06-01T00-00-00Z" {
		t.Fatalf("installed version = %q", result.InstalledVersion)
	}

	after, err := svc.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() after apply error = %v", err)
	}
	if after.UpdateAvailable {
		t.Fatalf("expected no update available after apply")
	}
	if !after.RestartRequired {
		t.Fatalf("expected restart required after apply")
	}
	if !strings.Contains(after.Reason, "Restart bm web") {
		t.Fatalf("reason = %q", after.Reason)
	}
}

func TestCheckDeniesApplyForDevBuild(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "bm")
	if err := os.WriteFile(target, []byte("dev binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	newBytes := []byte("new RELEASE.2026-06-01T00-00-00Z binary")
	sum := sha256.Sum256(newBytes)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/"+platform()+"/bm.sha256sum" {
			return textResponse(hex.EncodeToString(sum[:]) + "  bm.RELEASE.2026-06-01T00-00-00Z"), nil
		}
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Status:     "404 Not Found",
			Body:       io.NopCloser(strings.NewReader("not found")),
			Header:     make(http.Header),
		}, nil
	})}

	svc := &Service{
		Client:                       client,
		PointerBaseURL:               "https://updates.test",
		ReleasesBaseURL:              "https://updates.test",
		CurrentVersion:               "dev",
		TargetPath:                   target,
		DisableSignatureVerification: true,
	}

	status, err := svc.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if status.CanApply {
		t.Fatalf("expected apply to be denied for dev build")
	}
	if !strings.Contains(status.Reason, "Development build") {
		t.Fatalf("reason = %q", status.Reason)
	}
}

func binaryExt() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func textResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func bytesResponse(body []byte) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(strings.NewReader(string(body))),
		Header:     make(http.Header),
	}
}
