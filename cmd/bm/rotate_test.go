package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/buckit-io/bm/internal/store"
)

func TestRotatingWriterCapsAndKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bm.log")

	rw, err := newRotatingWriter(path, 100) // tiny cap for the test
	if err != nil {
		t.Fatal(err)
	}

	line := strings.Repeat("x", 30) + "\n" // 31 bytes
	for i := 0; i < 10; i++ {
		if _, err := rw.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	rw.Close()

	cur, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Size() > 100 {
		t.Fatalf("current log %d bytes exceeds cap 100", cur.Size())
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected backup bm.log.1 to exist: %v", err)
	}
	if cur.Size() == 0 {
		t.Fatal("current log unexpectedly empty after rotation")
	}
}

func TestRotatingWriterAppendsExistingSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bm.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("y", 90)), 0o600); err != nil {
		t.Fatal(err)
	}
	rw, err := newRotatingWriter(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer rw.Close()
	// Pre-existing 90 bytes + 30 > 100 should trigger a rollover, preserving
	// the old content in the backup.
	if _, err := rw.Write([]byte(strings.Repeat("z", 30))); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if !strings.HasPrefix(string(b), "yyy") {
		t.Fatalf("backup should hold the original content, got %q", string(b[:3]))
	}
}

func TestBusyStoreErrorIncludesShutdownHint(t *testing.T) {
	err := busyStoreError("127.0.0.1:9443", store.ErrBusy)
	msg := err.Error()

	if !errors.Is(err, store.ErrBusy) {
		t.Fatal("busyStoreError should wrap store.ErrBusy")
	}
	for _, want := range []string{
		"another bm process is using the database",
		"Open http://127.0.0.1:9443/",
		"click Stop Server",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q:\n%s", want, msg)
		}
	}
}
