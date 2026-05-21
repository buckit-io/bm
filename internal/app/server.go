// Package app wires the bm web process: server lifecycle, signal handling,
// graceful shutdown.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const shutdownDeadline = 5 * time.Second

// Serve starts srv and blocks until SIGINT/SIGTERM. On signal, it triggers a
// graceful shutdown with a 5s deadline. Returns the first non-nil error it
// sees; http.ErrServerClosed is swallowed (it's the expected exit).
func Serve(ctx context.Context, srv *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("listen: %w", err)
	case sig := <-sigCh:
		fmt.Fprintf(os.Stderr, "bm: received %s, shutting down…\n", sig)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownDeadline)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
