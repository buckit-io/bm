package localdeploy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/buckit-io/bm/internal/deploy"
	"github.com/buckit-io/bm/internal/domain"
)

func TestMain(m *testing.M) {
	restore := deploy.RestoreVersionsCacheForTest([]domain.BuckitVersion{{
		Tag:   "vtest",
		Label: "vtest",
		Artifacts: []domain.BuckitArtifact{
			{
				Kind:   "binary",
				OS:     "darwin",
				Arch:   "arm64",
				URL:    "https://example.test/buckit",
				SHA256: sha256Hex("fake-buckit"),
			},
			{
				Kind:   "binary",
				OS:     "windows",
				Arch:   "amd64",
				URL:    "https://example.test/buckit.exe",
				SHA256: sha256Hex("fake-buckit"),
			},
		},
	}})
	code := m.Run()
	restore()
	os.Exit(code)
}

func TestPrepareWritesDarwinScriptAndDownloadsBinary(t *testing.T) {
	home := t.TempDir()
	resp, err := Prepare(context.Background(), Request{
		Version:      "vtest",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths: []string{
			filepath.Join(home, "Buckit", "local", "data one"),
			filepath.Join(home, "Buckit", "local", "data2"),
		},
		TLS: domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64", Client: fakeDownloadClient("fake-buckit")})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if resp.APIURL != "http://127.0.0.1:9000" {
		t.Fatalf("unexpected api URL %q", resp.APIURL)
	}
	if resp.ConsoleURL != "http://127.0.0.1:9001" {
		t.Fatalf("unexpected console URL %q", resp.ConsoleURL)
	}
	bin, err := os.ReadFile(resp.BinaryPath)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	if string(bin) != "fake-buckit" {
		t.Fatalf("unexpected binary body %q", string(bin))
	}
	script, err := os.ReadFile(resp.ScriptPath)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	got := string(script)
	for _, want := range []string{
		"#!/bin/sh",
		"set -eu",
		"export MINIO_ROOT_USER='buckitadmin'",
		"data one'",
		"export MINIO_ROOTDRIVE_THRESHOLD_SIZE='1B'",
		"Storage class: ${MINIO_STORAGE_CLASS_STANDARD:-default}",
		"echo 'Data paths:'",
		"--address ':9000'",
		"--console-address ':9001'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("script missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--certs-dir") {
		t.Fatalf("did not expect --certs-dir when TLS is off:\n%s", got)
	}
	if strings.Contains(got, "export MINIO_ERASURE_SET_DRIVE_COUNT") || strings.Contains(got, "export MINIO_STORAGE_CLASS_STANDARD") {
		t.Fatalf("default erasure configuration should not be written:\n%s", got)
	}
	if strings.Contains(got, "mkdir -p") {
		t.Fatalf("script should not create data paths:\n%s", got)
	}
	if resp.Parity != 1 {
		t.Fatalf("unexpected parity %d", resp.Parity)
	}
	if resp.SetSize != 2 {
		t.Fatalf("unexpected set size %d", resp.SetSize)
	}
	for _, path := range resp.DataPaths {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("expected Prepare to create data path %s: info=%v err=%v", path, info, err)
		}
	}
}

func TestPrepareWritesTLSFilesAndHTTPSURLs(t *testing.T) {
	certPEM, keyPEM := genLocalCert(t)
	home := t.TempDir()
	resp, err := Prepare(context.Background(), Request{
		Version:      "vtest",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9443,
		ConsolePort:  9444,
		DataPaths:    []string{filepath.Join(home, "Buckit", "local", "data")},
		TLS: domain.TLSConfig{
			Mode:    domain.TLSBYO,
			CertPEM: certPEM,
			KeyPEM:  keyPEM,
		},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64", Client: fakeDownloadClient("fake-buckit")})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if resp.APIURL != "https://127.0.0.1:9443" {
		t.Fatalf("unexpected api URL %q", resp.APIURL)
	}
	certPath := filepath.Join(home, "buckit", "local", "certs", "public.crt")
	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("expected cert file: %v", err)
	}
	keyPath := filepath.Join(home, "buckit", "local", "certs", "private.key")
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("expected key file: %v", err)
	}
	script, err := os.ReadFile(resp.ScriptPath)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	if !strings.Contains(string(script), "--certs-dir") {
		t.Fatalf("script missing certs dir:\n%s", string(script))
	}
}

func TestPrepareKnownVersionVerifiesCatalogChecksum(t *testing.T) {
	home := t.TempDir()
	restore := deploy.RestoreVersionsCacheForTest([]domain.BuckitVersion{{
		Tag:   "vtest",
		Label: "vtest",
		Artifacts: []domain.BuckitArtifact{{
			Kind:   "binary",
			OS:     "darwin",
			Arch:   "arm64",
			URL:    "https://example.test/buckit",
			SHA256: sha256Hex("fake-buckit"),
		}},
	}})
	defer restore()

	_, err := Prepare(context.Background(), Request{
		Version:      "vtest",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths:    []string{filepath.Join(home, "Buckit", "local", "data")},
		TLS:          domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64", Client: fakeDownloadClient("fake-buckit")})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	restore = deploy.RestoreVersionsCacheForTest([]domain.BuckitVersion{{
		Tag:   "vbad",
		Label: "vbad",
		Artifacts: []domain.BuckitArtifact{{
			Kind:   "binary",
			OS:     "darwin",
			Arch:   "arm64",
			URL:    "https://example.test/buckit",
			SHA256: strings.Repeat("0", 64),
		}},
	}})
	defer restore()
	_, err = Prepare(context.Background(), Request{
		Version:      "vbad",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths:    []string{filepath.Join(home, "Buckit", "local", "data2")},
		TLS:          domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64", Client: fakeDownloadClient("fake-buckit")})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestPrepareRejectsOverlappingDataPaths(t *testing.T) {
	home := t.TempDir()
	_, err := Prepare(context.Background(), Request{
		Version:      "vtest",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths: []string{
			filepath.Join(home, "buckit", "local"),
			filepath.Join(home, "buckit", "local", "data"),
		},
		TLS: domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64", Client: fakeDownloadClient("fake-buckit")})
	if err == nil {
		t.Fatal("expected overlapping data paths error")
	}
	if !strings.Contains(err.Error(), "must not overlap") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareRejectsDataPathAtDeploymentRoot(t *testing.T) {
	home := t.TempDir()
	_, err := Prepare(context.Background(), Request{
		Version:      "vtest",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths:    []string{filepath.Join(home, "buckit", "local")},
		TLS:          domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64", Client: fakeDownloadClient("fake-buckit")})
	if err == nil {
		t.Fatal("expected deployment root overlap error")
	}
	if !strings.Contains(err.Error(), "local deployment directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareRejectsCustomVersion(t *testing.T) {
	home := t.TempDir()
	_, err := Prepare(context.Background(), Request{
		Version:      "custom",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths:    []string{filepath.Join(home, "Buckit", "local", "data")},
		TLS:          domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64", Client: fakeDownloadClient("fake-buckit")})
	if err == nil || !strings.Contains(err.Error(), "does not support custom binary URLs") {
		t.Fatalf("expected custom version error, got %v", err)
	}
}

func TestPrepareRejectsChecksumMismatch(t *testing.T) {
	home := t.TempDir()
	restore := deploy.RestoreVersionsCacheForTest([]domain.BuckitVersion{{
		Tag:   "vbad",
		Label: "vbad",
		Artifacts: []domain.BuckitArtifact{{
			Kind:   "binary",
			OS:     "darwin",
			Arch:   "arm64",
			URL:    "https://example.test/buckit",
			SHA256: strings.Repeat("0", 64),
		}},
	}})
	defer restore()
	_, err := Prepare(context.Background(), Request{
		Version:      "vbad",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths:    []string{filepath.Join(home, "Buckit", "local", "data")},
		TLS:          domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64", Client: fakeDownloadClient("fake-buckit")})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestPrepareRejectsNonEmptyDataPath(t *testing.T) {
	home := t.TempDir()
	dataPath := filepath.Join(home, "Buckit", "local", "data")
	if err := os.MkdirAll(dataPath, 0o700); err != nil {
		t.Fatalf("mkdir data path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataPath, "existing-object"), []byte("data"), 0o600); err != nil {
		t.Fatalf("write existing file: %v", err)
	}
	_, err := Prepare(context.Background(), Request{
		Version:      "vtest",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths:    []string{dataPath},
		TLS:          domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64", Client: fakeDownloadClient("fake-buckit")})
	if err == nil || !strings.Contains(err.Error(), "already exists and is not empty") {
		t.Fatalf("expected non-empty data path error, got %v", err)
	}
}

func TestPreviewWarnsWhenBinaryExists(t *testing.T) {
	home := t.TempDir()
	binaryPath := filepath.Join(home, ".local", "bin", "buckit")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o700); err != nil {
		t.Fatalf("mkdir binary dir: %v", err)
	}
	if err := os.WriteFile(binaryPath, []byte("old-binary"), 0o700); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	resp, err := Preview(Request{
		Version:      "vtest",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths:    []string{filepath.Join(home, "Buckit", "local", "data")},
		TLS:          domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64"})
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if resp.BinaryPath != binaryPath {
		t.Fatalf("unexpected binary path %q", resp.BinaryPath)
	}
	if !warningsContain(resp.Warnings, "will be overwritten") {
		t.Fatalf("expected overwrite warning, got %#v", resp.Warnings)
	}
}

func TestPrepareOmitsOverwrittenBinaryWarning(t *testing.T) {
	home := t.TempDir()
	binaryPath := filepath.Join(home, ".local", "bin", "buckit")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o700); err != nil {
		t.Fatalf("mkdir binary dir: %v", err)
	}
	if err := os.WriteFile(binaryPath, []byte("old-binary"), 0o700); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	resp, err := Prepare(context.Background(), Request{
		Version:      "vtest",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths:    []string{filepath.Join(home, "Buckit", "local", "data")},
		TLS:          domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64", Client: fakeDownloadClient("fake-buckit")})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if warningsContain(resp.Warnings, "will be overwritten") {
		t.Fatalf("did not expect overwrite warning after Prepare, got %#v", resp.Warnings)
	}
	body, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	if string(body) != "fake-buckit" {
		t.Fatalf("expected binary to be replaced, got %q", string(body))
	}
}

func TestPrepareUsesCustomParity(t *testing.T) {
	home := t.TempDir()
	resp, err := Prepare(context.Background(), Request{
		Version:      "vtest",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths:    []string{"~/buckit/local/data{1...4}"},
		Parity:       1,
		TLS:          domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64", Client: fakeDownloadClient("fake-buckit")})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if resp.Parity != 1 {
		t.Fatalf("unexpected parity %d", resp.Parity)
	}
	if resp.SetSize != 4 {
		t.Fatalf("unexpected set size %d", resp.SetSize)
	}
	script, err := os.ReadFile(resp.ScriptPath)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	if strings.Contains(string(script), "export MINIO_ERASURE_SET_DRIVE_COUNT") {
		t.Fatalf("script should not override the default erasure set size:\n%s", string(script))
	}
	if !strings.Contains(string(script), "export MINIO_STORAGE_CLASS_STANDARD='EC:1'") {
		t.Fatalf("script missing storage class parity:\n%s", string(script))
	}
}

func TestPrepareRejectsInvalidParity(t *testing.T) {
	home := t.TempDir()
	_, err := Prepare(context.Background(), Request{
		Version:      "vtest",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths:    []string{"~/buckit/local/data{1...4}"},
		Parity:       3,
		TLS:          domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64", Client: fakeDownloadClient("fake-buckit")})
	if err == nil || !strings.Contains(err.Error(), "parity must be between 1 and 2") {
		t.Fatalf("expected parity error, got %v", err)
	}

	_, err = Prepare(context.Background(), Request{
		Version:      "vtest",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths:    []string{filepath.Join(home, "Buckit", "local", "data")},
		Parity:       1,
		TLS:          domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64", Client: fakeDownloadClient("fake-buckit")})
	if err == nil || !strings.Contains(err.Error(), "requires at least two data paths") {
		t.Fatalf("expected standalone parity error, got %v", err)
	}
}

func TestPrepareRejectsUnsupportedErasurePathCount(t *testing.T) {
	home := t.TempDir()
	_, err := Prepare(context.Background(), Request{
		Version:      "vtest",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths:    []string{"~/buckit/local/data{1...17}"},
		TLS:          domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64", Client: fakeDownloadClient("fake-buckit")})
	if err == nil || !strings.Contains(err.Error(), "not divisible by any supported erasure set size") {
		t.Fatalf("expected path count error, got %v", err)
	}
}

func TestPrepareExpandsDataPathPatterns(t *testing.T) {
	home := t.TempDir()
	resp, err := Prepare(context.Background(), Request{
		Version:      "vtest",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths:    []string{"~/buckit/local/data{01...02}"},
		TLS:          domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64", Client: fakeDownloadClient("fake-buckit")})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	want := []string{
		filepath.Join(home, "buckit", "local", "data01"),
		filepath.Join(home, "buckit", "local", "data02"),
	}
	if strings.Join(resp.DataPaths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("data paths:\n got %v\nwant %v", resp.DataPaths, want)
	}
	script, err := os.ReadFile(resp.ScriptPath)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	got := string(script)
	for _, path := range want {
		if !strings.Contains(got, "'"+path+"'") {
			t.Fatalf("script missing expanded path %s:\n%s", path, got)
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("expected Prepare to create expanded path %s: info=%v err=%v", path, info, err)
		}
	}
	if strings.Contains(got, "mkdir -p") {
		t.Fatalf("script should not create data paths:\n%s", got)
	}
}

func TestPrepareRejectsInvalidDataPathPattern(t *testing.T) {
	home := t.TempDir()
	_, err := Prepare(context.Background(), Request{
		Version:      "vtest",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths:    []string{"~/buckit/local/data{4...1}"},
		TLS:          domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64", Client: fakeDownloadClient("fake-buckit")})
	if err == nil || !strings.Contains(err.Error(), "range must ascend") {
		t.Fatalf("expected range error, got %v", err)
	}
}

func TestPrepareWarnsForMultiplePathsOnRootDrive(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("root-drive detection for local deployment is only active on darwin in this test")
	}
	home := t.TempDir()
	resp, err := Prepare(context.Background(), Request{
		Version:      "vtest",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths: []string{
			filepath.Join(home, "data1"),
			filepath.Join(home, "data2"),
		},
		TLS: domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64", Client: fakeDownloadClient("fake-buckit")})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if !warningsContain(resp.Warnings, "root/OS drive") {
		t.Fatalf("expected root-drive warning, got %#v", resp.Warnings)
	}
}

func TestPrepareWritesWindowsScriptAndQuotesValues(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	resp, err := Prepare(context.Background(), Request{
		Version:      "vtest",
		RootUser:     "buckitadmin",
		RootPassword: "secret';$(bad)#CHANGE",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths: []string{
			filepath.Join(home, "Buckit", "local", "Data One"),
			filepath.Join(home, "Buckit", "local", "data2"),
		},
		TLS: domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "windows", GOARCH: "amd64", Client: fakeDownloadClient("fake-buckit")})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if !strings.Contains(resp.Command, "powershell -ExecutionPolicy Bypass -File ") {
		t.Fatalf("unexpected command %q", resp.Command)
	}
	script, err := os.ReadFile(resp.ScriptPath)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	got := string(script)
	for _, want := range []string{
		"$ErrorActionPreference = 'Stop'",
		"$env:MINIO_ROOT_USER = 'buckitadmin'",
		"$env:MINIO_ROOT_PASSWORD = 'secret'';$(bad)#CHANGE'",
		"$env:MINIO_ROOTDRIVE_THRESHOLD_SIZE = '1B'",
		"$Volumes = @(",
		"Write-Host \"Storage class:",
		"Write-Host 'Data paths:'",
		"server @Volumes --address ':9000' --console-address ':9001'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("script missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "New-Item") {
		t.Fatalf("script should not create data paths:\n%s", got)
	}
	if strings.Contains(got, "$env:MINIO_ERASURE_SET_DRIVE_COUNT") || strings.Contains(got, "$env:MINIO_STORAGE_CLASS_STANDARD =") {
		t.Fatalf("default erasure configuration should not be written:\n%s", got)
	}
}

func TestPrepareWritesShellScriptAndQuotesValues(t *testing.T) {
	home := t.TempDir()
	resp, err := Prepare(context.Background(), Request{
		Version:      "vtest",
		RootUser:     "buckitadmin",
		RootPassword: "secret';$(bad)#CHANGE",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths:    []string{filepath.Join(home, "Buckit", "local", "data")},
		TLS:          domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "darwin", GOARCH: "arm64", Client: fakeDownloadClient("fake-buckit")})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	script, err := os.ReadFile(resp.ScriptPath)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	got := string(script)
	for _, want := range []string{
		"export MINIO_ROOT_USER='buckitadmin'",
		"export MINIO_ROOT_PASSWORD='secret'\\'';$(bad)#CHANGE'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("script missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "MINIO_ROOTDRIVE_THRESHOLD_SIZE") {
		t.Fatalf("single-path script should not set root drive threshold:\n%s", got)
	}
	if strings.Contains(got, "export MINIO_STORAGE_CLASS_STANDARD") {
		t.Fatalf("single-path script should not set storage class parity:\n%s", got)
	}
	if strings.Contains(got, "MINIO_ERASURE_SET_DRIVE_COUNT") {
		t.Fatalf("single-path script should not set erasure set size:\n%s", got)
	}
	if strings.Contains(got, "mkdir -p") {
		t.Fatalf("script should not create data paths:\n%s", got)
	}
	if info, err := os.Stat(resp.DataPaths[0]); err != nil || !info.IsDir() {
		t.Fatalf("expected Prepare to create data path %s: info=%v err=%v", resp.DataPaths[0], info, err)
	}
}

func TestPrepareRejectsUnsupportedOS(t *testing.T) {
	home := t.TempDir()
	_, err := Prepare(context.Background(), Request{
		Version:      "vtest",
		RootUser:     "buckitadmin",
		RootPassword: "buckit-secret-key-CHANGE-ME",
		APIPort:      9000,
		ConsolePort:  9001,
		DataPaths:    []string{filepath.Join(home, "Buckit", "local", "data")},
		TLS:          domain.TLSConfig{Mode: domain.TLSOff},
	}, Options{HomeDir: home, GOOS: "linux", GOARCH: "amd64", Client: fakeDownloadClient("fake-buckit")})
	if err == nil || !strings.Contains(err.Error(), "not linux") {
		t.Fatalf("expected unsupported OS error, got %v", err)
	}
}

func fakeDownloadClient(body string) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		}),
	}
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func warningsContain(warnings []string, needle string) bool {
	for _, w := range warnings {
		if strings.Contains(w, needle) {
			return true
		}
	}
	return false
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func genLocalCert(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	return string(certPEM), string(keyPEM)
}
