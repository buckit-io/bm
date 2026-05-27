package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
)

const defaultAddr = "127.0.0.1:9443"

func runWeb(rawArgs []string) error {
	fs := flag.NewFlagSet("bm web", flag.ContinueOnError)
	addr := fs.String("addr", defaultAddr, "listen address (loopback only in M1)")
	noBrowser := fs.Bool("no-browser", false, "do not open the default browser on startup")
	dataDir := fs.String("data-dir", "", "override config dir (default: ~/.config/bm)")
	webDist := fs.String("web-dist", defaultWebDist(), "override embedded UI assets with a built web/dist directory")
	if err := fs.Parse(rawArgs); err != nil {
		return err
	}

	if err := api.AssertLoopback(*addr); err != nil {
		return err
	}

	paths, err := config.Resolve(*dataDir)
	if err != nil {
		return err
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
		AfterCommit: func(ctx context.Context, _ string) {
			syncCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_ = alias.Sync(syncCtx, st, paths.AliasFile)
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
		WebDist:      *webDist,
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

	return app.Serve(context.Background(), srv)
}

func defaultWebDist() string {
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
