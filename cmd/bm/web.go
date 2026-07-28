package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/buckit-io/bm/internal/admin"
	"github.com/buckit-io/bm/internal/alias"
	"github.com/buckit-io/bm/internal/api"
	"github.com/buckit-io/bm/internal/app"
	"github.com/buckit-io/bm/internal/clusteradmin"
	"github.com/buckit-io/bm/internal/clusters"
	"github.com/buckit-io/bm/internal/config"
	"github.com/buckit-io/bm/internal/deploy"
	"github.com/buckit-io/bm/internal/migration"
	"github.com/buckit-io/bm/internal/nodes"
	"github.com/buckit-io/bm/internal/operations"
	"github.com/buckit-io/bm/internal/preflight"
	bmssh "github.com/buckit-io/bm/internal/ssh"
	"github.com/buckit-io/bm/internal/sshconfig"
	"github.com/buckit-io/bm/internal/store"
	"github.com/buckit-io/bm/internal/tasks"
	"github.com/buckit-io/bm/internal/update"
	"github.com/buckit-io/bm/internal/version"
)

const defaultAddr = "127.0.0.1:9443"

func runWeb(rawArgs []string) error {
	fs := flag.NewFlagSet("bm web", flag.ContinueOnError)
	addr := fs.String("addr", defaultAddr, "listen address (loopback only in M1)")
	allowNonLoopback := fs.Bool("allow-non-loopback", false, "allow non-loopback listen addresses for local test/lab use")
	noBrowser := fs.Bool("no-browser", false, "do not open the default browser on startup")
	dataDir := fs.String("data-dir", "", "override config dir (default: ~/.config/bm)")
	logFile := fs.String("log-file", "", "write a copy of stdout/stderr to this file (default: <data-dir>/bm.log)")
	noLogFile := fs.Bool("no-log-file", false, "do not write a log file (stdout/stderr stay on the terminal only)")
	webDist := fs.String("web-dist", defaultWebDist(), "override embedded UI assets with a built web/dist directory")
	if err := fs.Parse(rawArgs); err != nil {
		return err
	}

	if !*allowNonLoopback {
		if err := api.AssertLoopback(*addr); err != nil {
			return err
		}
	}
	if *allowNonLoopback && *addr == defaultAddr {
		fmt.Fprintf(os.Stderr, "bm: --allow-non-loopback set but %s is already loopback-only\n", *addr)
	}

	paths, err := config.Resolve(*dataDir)
	if err != nil {
		return err
	}

	if !*noLogFile {
		logPath := *logFile
		if logPath == "" {
			logPath = filepath.Join(paths.Dir, "bm.log")
		}
		// Print the banner to the real terminal before swapping the streams,
		// so it's the one line the operator sees; everything else goes to file.
		fmt.Fprintf(os.Stderr, "bm: output is saved to log file at %s\n", logPath)
		stopLogging, err := startFileLogging(logPath)
		if err != nil {
			return fmt.Errorf("open log file %s: %w", logPath, err)
		}
		defer stopLogging()
	}

	key, generated, err := config.LoadDataKey(paths)
	if err != nil {
		return err
	}
	if generated {
		fmt.Fprintf(os.Stderr, "bm: generated new data key at %s\n", paths.DataKey)
	}
	st, err := store.Open(paths.DBFile, key)
	if err != nil {
		if errors.Is(err, store.ErrBusy) {
			return busyStoreError(*addr, err)
		}
		return err
	}
	defer st.Close()

	syncCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := alias.Sync(syncCtx, st, paths.AliasFile); err != nil {
		cancel()
		return fmt.Errorf("alias sync: %w", err)
	}
	cancel()

	nodesRepo := nodes.New(st)
	sshcfgRepo := sshconfig.New(st)
	sshPool := bmssh.NewPool(nil)
	defer sshPool.Close()

	clustersRepo := clusters.New(st)
	clusterAdminRepo := clusteradmin.New(st)
	adminPool := admin.NewPool()

	// Wire the preflight package's version resolver to the deploy catalog so
	// the rpm + artifact_reachable checks can resolve tag→URL.
	preflight.SetVersionResolver(func(tag string) string {
		if v := deploy.VersionByTag(tag); v != nil {
			return v.RpmURL
		}
		return ""
	})

	mgr := tasks.NewManager(st)
	tasks.RegisterNoop()
	tasks.RegisterSshProbe(nodesRepo, sshcfgRepo)
	deploy.Register(&deploy.Executor{
		Installer:    deploy.NewInstaller(sshPool),
		Clusters:     clustersRepo,
		Nodes:        nodesRepo,
		ClusterAdmin: clusterAdminRepo,
		SSHConfig:    sshcfgRepo,
		AfterCommit: func(ctx context.Context, _ string) error {
			syncCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return alias.Sync(syncCtx, st, paths.AliasFile)
		},
	})

	// M7: register the full operations catalog (signal, admin-with-progression,
	// orchestrated, host-scoped).
	operations.RegisterAll(operations.Deps{
		Clusters:     clustersRepo,
		Nodes:        nodesRepo,
		ClusterAdmin: clusterAdminRepo,
		SSHConfig:    sshcfgRepo,
		AdminPool:    adminPool,
		SSHPool:      sshPool,
	})

	// M8: register the migration cutover + rollback executors.
	migration.Register(migration.Deps{
		Clusters:     clustersRepo,
		ClusterAdmin: clusterAdminRepo,
		AdminPool:    adminPool,
		SSHPool:      sshPool,
	})
	recoveryCtx, cancelRecovery := context.WithTimeout(context.Background(), 5*time.Second)
	recovered, err := tasks.RecoverInFlight(recoveryCtx, mgr)
	cancelRecovery()
	if err != nil {
		return fmt.Errorf("recover in-flight ops: %w", err)
	}
	if recovered > 0 {
		fmt.Fprintf(os.Stderr, "bm: marked %d in-flight op(s) as failed after restart\n", recovered)
	}
	defer mgr.Shutdown()

	serveCtx, requestShutdown := context.WithCancel(context.Background())
	defer requestShutdown()
	var shutdownOnce sync.Once

	handler := api.New(api.Options{
		Store:        st,
		Tasks:        mgr,
		Nodes:        nodesRepo,
		SSHConfig:    sshcfgRepo,
		SSHPool:      sshPool,
		Clusters:     clustersRepo,
		ClusterAdmin: clusterAdminRepo,
		AdminPool:    adminPool,
		AliasPath:    paths.AliasFile,
		Updater:      update.NewService(),
		WebDist:      *webDist,
		Shutdown: func() {
			shutdownOnce.Do(func() {
				fmt.Fprintln(os.Stderr, "bm: shutdown requested from web UI")
				requestShutdown()
			})
		},
	})
	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	url := fmt.Sprintf("http://%s/", *addr)
	fmt.Fprintf(os.Stderr, "bm web: serving on %s (data dir: %s)\n", url, paths.Dir)
	if !*noBrowser {
		openBrowser(url)
	}

	return app.Serve(serveCtx, srv)
}

func busyStoreError(addr string, err error) error {
	url := fmt.Sprintf("http://%s/", addr)
	return fmt.Errorf("%w\n\nA Buckit Manager web server is probably already running.\nOpen %s and click Exit to stop the instance.", err, url)
}

// maxLogBytes caps bm.log: once a write would push it past this size the file
// is rolled over to "<path>.1" (one backup, overwritten on each roll) and a
// fresh file is started. At most maxLogBytes×2 of log data lives on disk.
const maxLogBytes = 10 << 20 // 10 MiB

// startFileLogging redirects os.Stdout and os.Stderr into logPath (opened in
// append mode). It works by swapping the global streams for OS pipes and
// copying each pipe into the shared log file. Nothing is echoed back to the
// terminal — all of bm web's output goes to the file only.
//
// The returned cleanup func restores the original streams, drains the pipes,
// and closes the file; callers should defer it for the lifetime of the process.
func startFileLogging(logPath string) (func(), error) {
	lf, err := newRotatingWriter(logPath, maxLogBytes)
	if err != nil {
		return nil, err
	}

	outR, outW, err := os.Pipe()
	if err != nil {
		lf.Close()
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		lf.Close()
		return nil, err
	}

	origStdout, origStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW

	var wg sync.WaitGroup
	wg.Add(2)
	// Both streams go to the log file only; nothing is echoed to the terminal.
	go func() { defer wg.Done(); _, _ = io.Copy(lf, outR) }()
	go func() { defer wg.Done(); _, _ = io.Copy(lf, errR) }()

	return func() {
		os.Stdout, os.Stderr = origStdout, origStderr
		// Closing the write ends makes the copiers see EOF and return.
		outW.Close()
		errW.Close()
		wg.Wait()
		outR.Close()
		errR.Close()
		lf.Close()
	}, nil
}

// rotatingWriter is the shared log-file sink. Its mutex both serializes the
// concurrent writes from the stdout and stderr copiers (so lines don't
// interleave byte-wise) and guards the rollover bookkeeping.
type rotatingWriter struct {
	mu   sync.Mutex
	f    *os.File
	path string
	max  int64
	size int64
}

func newRotatingWriter(path string, max int64) (*rotatingWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	var size int64
	if info, statErr := f.Stat(); statErr == nil {
		size = info.Size()
	}
	return &rotatingWriter{f: f, path: path, max: max, size: size}, nil
}

func (rw *rotatingWriter) Write(p []byte) (int, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.size > 0 && rw.size+int64(len(p)) > rw.max {
		// Best effort: if rollover fails we keep appending to the current file
		// rather than dropping the line.
		_ = rw.rotate()
	}
	n, err := rw.f.Write(p)
	rw.size += int64(n)
	return n, err
}

// rotate closes the current file, renames it to "<path>.1" (replacing any
// previous backup), and opens a fresh empty file at path.
func (rw *rotatingWriter) rotate() error {
	if err := rw.f.Close(); err != nil {
		return err
	}
	backup := rw.path + ".1"
	_ = os.Remove(backup) // Rename won't overwrite an existing target on Windows.
	if err := os.Rename(rw.path, backup); err != nil {
		// Rename failed; reopen the original so logging continues.
		rw.f, _ = os.OpenFile(rw.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		return err
	}
	f, err := os.OpenFile(rw.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	rw.f = f
	rw.size = 0
	return nil
}

func (rw *rotatingWriter) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	return rw.f.Close()
}

func defaultWebDist() string {
	// Released binaries carry their UI inside the executable. Do not
	// accidentally select an adjacent web/dist left behind by an earlier
	// development install: it can contain index.html without the matching
	// hashed assets, causing the browser to receive the SPA fallback for every
	// JavaScript module.
	if strings.HasPrefix(version.Version, "RELEASE.") {
		return ""
	}

	// Prefer an adjacent web/dist during local development so contributors
	// see current frontend changes without rebuilding the binary first.
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "web", "dist")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(wd, "web", "dist")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	return ""
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			fmt.Fprintf(os.Stderr, "bm: could not open browser (%v); navigate to %s manually\n", err, url)
			return
		}
		fmt.Fprintf(os.Stderr, "bm: open browser failed: %v\n", err)
	}
}
