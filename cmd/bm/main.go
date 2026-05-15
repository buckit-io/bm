// Command bm is the Buckit Manager — a personal desktop tool that wraps
// the operator's Buckit clusters with a CLI plus an optional local web UI.
//
// Subcommands:
//
//	bm web         start the local web UI (foreground; opens default browser)
//	bm version     print the build version
//	bm help        print usage
//
// Phase 1 ships `bm web` plus a minimal read-only CLI surface. Richer
// `mc`-style write commands land in Phase 2 (see
// buckit/docs/manager/README.md).
package main

import (
	"fmt"
	"os"

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
	case "help", "--help", "-h":
		usage(os.Stdout)
	case "web", "server": // "server" kept as a hidden alias for muscle memory
		fmt.Fprintln(os.Stderr, "bm web: not yet implemented (M1)")
		os.Exit(2)
	default:
		fmt.Fprintf(os.Stderr, "bm: unknown subcommand %q\n\n", args[0])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprintf(w, `bm — Buckit Manager

Usage:
  bm <command> [flags]

Commands:
  web       Start the local web UI (foreground; opens default browser)
  version   Print the build version
  help      Print this message

Run 'bm <command> --help' for command-specific help.
`)
}
