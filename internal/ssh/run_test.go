package ssh

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/buckit-io/bm/internal/domain"
)

func TestRunStdoutStderrAndExit(t *testing.T) {
	srv := newTestServer(t)
	host, port := srv.HostPort()
	client, err := Dial(context.Background(), domain.HostRef{Hostname: host, Port: port}, Resolved{
		AuthMethod: domain.AuthPassword,
		User:       srv.user,
		Password:   srv.password,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	res, err := Run(context.Background(), client, "echo hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "hello" || res.ExitCode != 0 {
		t.Fatalf("unexpected: %+v", res)
	}

	res, err = Run(context.Background(), client, "fail")
	if err != nil {
		t.Fatalf("Run fail: %v", err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("want exit 7, got %d", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "failed") {
		t.Fatalf("missing stderr: %q", res.Stderr)
	}
}

func TestRunStreamLines(t *testing.T) {
	srv := newTestServer(t)
	host, port := srv.HostPort()
	client, err := Dial(context.Background(), domain.HostRef{Hostname: host, Port: port}, Resolved{
		AuthMethod: domain.AuthPassword,
		User:       srv.user,
		Password:   srv.password,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	out := make(chan Line, 16)
	exitCh := make(chan int, 1)
	errCh := make(chan error, 1)
	go func() {
		code, err := RunStream(context.Background(), client, "stream-3", out)
		exitCh <- code
		errCh <- err
	}()

	var got []Line
	for line := range out {
		got = append(got, line)
	}
	if code := <-exitCh; code != 0 {
		t.Fatalf("want exit 0, got %d", code)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if len(got) < 4 {
		t.Fatalf("want at least 4 lines, got %d (%+v)", len(got), got)
	}
}

func TestRunCancel(t *testing.T) {
	srv := newTestServer(t)
	host, port := srv.HostPort()
	client, err := Dial(context.Background(), domain.HostRef{Hostname: host, Port: port}, Resolved{
		AuthMethod: domain.AuthPassword,
		User:       srv.user,
		Password:   srv.password,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = Run(ctx, client, "sleep 10")
	if err == nil {
		t.Fatal("expected context error")
	}
}

func TestRunAppliesDefaultTimeoutWhenContextHasNoDeadline(t *testing.T) {
	srv := newTestServer(t)
	host, port := srv.HostPort()
	client, err := Dial(context.Background(), domain.HostRef{Hostname: host, Port: port}, Resolved{
		AuthMethod: domain.AuthPassword,
		User:       srv.user,
		Password:   srv.password,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	prev := defaultCommandTimeout
	defaultCommandTimeout = 200 * time.Millisecond
	defer func() { defaultCommandTimeout = prev }()

	start := time.Now()
	_, err = Run(context.Background(), client, "sleep 10")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("default timeout did not fire promptly; elapsed=%s", elapsed)
	}
}

func TestRunCallerDeadlineDisablesDefaultTimeout(t *testing.T) {
	srv := newTestServer(t)
	host, port := srv.HostPort()
	client, err := Dial(context.Background(), domain.HostRef{Hostname: host, Port: port}, Resolved{
		AuthMethod: domain.AuthPassword,
		User:       srv.user,
		Password:   srv.password,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	prev := defaultCommandTimeout
	defaultCommandTimeout = 50 * time.Millisecond
	defer func() { defaultCommandTimeout = prev }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	res, err := Run(ctx, client, "sleep 0.2")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("want exit 0, got %d", res.ExitCode)
	}
}

func TestRunWithoutDefaultTimeout(t *testing.T) {
	srv := newTestServer(t)
	host, port := srv.HostPort()
	client, err := Dial(context.Background(), domain.HostRef{Hostname: host, Port: port}, Resolved{
		AuthMethod: domain.AuthPassword,
		User:       srv.user,
		Password:   srv.password,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	prev := defaultCommandTimeout
	defaultCommandTimeout = 50 * time.Millisecond
	defer func() { defaultCommandTimeout = prev }()

	ctx, cancel := context.WithTimeout(WithoutDefaultTimeout(context.Background()), time.Second)
	defer cancel()

	res, err := Run(ctx, client, "sleep 0.2")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("want exit 0, got %d", res.ExitCode)
	}
}
