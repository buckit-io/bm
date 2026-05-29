package ssh

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/ssh"
)

const sessionCancelGrace = 2 * time.Second

var defaultCommandTimeout = 90 * time.Second

type defaultTimeoutDisabledKey struct{}

// Line is one stdout or stderr line emitted by RunStream.
type Line struct {
	Stream string // "stdout" | "stderr"
	Text   string
}

// Result captures the outcome of a one-shot Run.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// WithoutDefaultTimeout opts out of the SSH package's fallback per-command
// timeout. Callers should prefer passing their own bounded context; this is
// only for flows that intentionally manage timing outside Run/RunStream.
func WithoutDefaultTimeout(ctx context.Context) context.Context {
	return context.WithValue(ctx, defaultTimeoutDisabledKey{}, true)
}

// Run executes cmd on client and returns the captured stdout, stderr, and
// exit code. If ctx has no deadline, Run applies a fallback command timeout.
// Honors ctx by closing the session on cancellation. Returns the Result even
// on non-zero exit; err is non-nil only for transport / setup failures or
// ctx cancellation.
func Run(ctx context.Context, client *ssh.Client, cmd string) (Result, error) {
	if client == nil {
		return Result{}, errors.New("ssh: nil client")
	}
	ctx, cancel := withDefaultCommandTimeout(ctx)
	defer cancel()

	session, err := client.NewSession()
	if err != nil {
		return Result{}, fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	done := make(chan error, 1)
	go func() { done <- session.Run(cmd) }()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		_ = session.Close()
		waitForRunDone(done)
		return Result{Stdout: stdout.String(), Stderr: stderr.String()}, ctx.Err()
	case err := <-done:
		res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
		if err == nil {
			return res, nil
		}
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitStatus()
			return res, nil
		}
		return res, fmt.Errorf("run %q: %w", cmd, err)
	}
}

// RunStream executes cmd and streams stdout+stderr lines to out. If ctx has
// no deadline, RunStream applies the fallback command timeout. The channel is
// closed when the command exits or ctx is canceled. Returns the exit code
// (or -1 on transport error) and the first non-nil error encountered.
func RunStream(ctx context.Context, client *ssh.Client, cmd string, out chan<- Line) (int, error) {
	defer close(out)
	if client == nil {
		return -1, errors.New("ssh: nil client")
	}
	ctx, cancel := withDefaultCommandTimeout(ctx)
	defer cancel()

	session, err := client.NewSession()
	if err != nil {
		return -1, fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	stdout, err := session.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return -1, err
	}
	if err := session.Start(cmd); err != nil {
		return -1, fmt.Errorf("start %q: %w", cmd, err)
	}

	pumpDone := make(chan struct{}, 2)
	go pumpLines(ctx, stdout, "stdout", out, pumpDone)
	go pumpLines(ctx, stderr, "stderr", out, pumpDone)

	waitDone := make(chan error, 1)
	go func() { waitDone <- session.Wait() }()

	var waitErr error
	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		_ = session.Close()
		waitErr = ctx.Err()
		waitForRunDone(waitDone)
	case waitErr = <-waitDone:
	}
	waitForPumpDone(pumpDone)
	waitForPumpDone(pumpDone)

	if waitErr == nil {
		return 0, nil
	}
	var exitErr *ssh.ExitError
	if errors.As(waitErr, &exitErr) {
		return exitErr.ExitStatus(), nil
	}
	if errors.Is(waitErr, context.Canceled) || errors.Is(waitErr, context.DeadlineExceeded) {
		return -1, waitErr
	}
	return -1, waitErr
}

func waitForRunDone(done <-chan error) {
	select {
	case <-done:
	case <-time.After(sessionCancelGrace):
	}
}

func withDefaultCommandTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	if disabled, ok := ctx.Value(defaultTimeoutDisabledKey{}).(bool); ok && disabled {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, defaultCommandTimeout)
}

func waitForPumpDone(done <-chan struct{}) {
	select {
	case <-done:
	case <-time.After(sessionCancelGrace):
	}
}

func pumpLines(ctx context.Context, r io.Reader, stream string, out chan<- Line, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		select {
		case <-ctx.Done():
			return
		case out <- Line{Stream: stream, Text: sc.Text()}:
		}
	}
}
