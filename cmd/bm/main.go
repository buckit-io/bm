// Command bm is the Buckit Manager — a personal desktop tool that wraps the
// operator's Buckit clusters with a CLI plus an optional local web UI.
//
// Subcommands fall into two camps:
//
//   - bm-native verbs are written fresh in cmd/bm/. Today: `web`, `version`,
//     `help`. The rest land in their owning milestones (cluster, manager,
//     migrate, rolling, node, history, settings).
//
//   - Everything else (cp, ls, mb, alias, admin *, …) comes unchanged from
//     the forked buckit-io/bm-cli and is invoked by handing the args off to
//     internal/bmcli.Delegate.
package main

import (
	"fmt"
	"os"

	"github.com/buckit-io/bm/internal/bmcli"
	"github.com/buckit-io/bm/internal/config"
	"github.com/buckit-io/bm/internal/version"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage(os.Stdout)
		return
	}

	switch args[0] {
	case "version", "--version", "-v":
		fmt.Println(version.String())
		return
	case "help", "--help", "-h":
		usage(os.Stdout)
		return
	case "web", "server": // "server" kept as a hidden alias for muscle memory
		if err := runWeb(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "bm web: %v\n", err)
			os.Exit(1)
		}
		return
	case "update":
		if err := runUpdate(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "bm update: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Hand off everything else to the forked bm-cli. We swap in the bm
	// config dir so `bm alias list` (etc.) read from ~/.config/bm/config.json
	// instead of ~/.mc/config.json.
	paths, err := config.Resolve("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "bm: %v\n", err)
		os.Exit(1)
	}
	if err := bmcli.Delegate(os.Args, paths.Dir); err != nil {
		os.Exit(1)
	}
}

func usage(w *os.File) {
	fmt.Fprintf(w, `bm — Buckit Manager

Usage:
  bm <command> [flags]

Native commands:
  web       Start the local web UI (foreground; opens default browser)
  update    Check for or apply a bm self-update
  version   Print the build version
  help      Print this message

All other Buckit CLI verbs (cp, ls, mb, alias, admin *, ...) come from the
forked buckit-io/bm-cli. Run 'bm <command> --help' for command-specific help.
`)
}
