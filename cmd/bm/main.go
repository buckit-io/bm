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
		printHelp()
		return
	}

	switch args[0] {
	case "version", "--version", "-v":
		fmt.Println(version.String())
		return
	case "help", "--help", "-h":
		printHelp()
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

Buckit Manager commands:
  web        Start the local web UI (foreground; opens default browser)
  update     Check for or apply a bm self-update
  version    Print the build version
  help       Print this message

Cluster Management commands:
  alias      manage server credentials in configuration file
  admin      manage Buckit servers
  batch      manage batch jobs
  idp        manage Buckit IDentity Provider server configuration
  license    license related commands
  ping       perform liveness check
  ready      check whether a cluster is ready
  support    support-related commands

Object Storage commands:
  anonymous  manage anonymous access to buckets and objects
  cat        display object contents
  cors       manage bucket CORS configuration
  cp         copy objects
  diff       list differences in name, size, and date between two buckets
  du         summarize disk usage recursively
  encrypt    manage bucket encryption config
  event      manage object notifications
  find       search for objects
  get        get s3 object to local
  head       display first 'n' lines of an object
  ilm        manage bucket lifecycle
  legalhold  manage legal hold for object(s)
  ls         list buckets and objects
  mb         make a bucket
  mirror     synchronize object(s) to a remote site
  mv         move objects
  od         measure single-stream upload and download
  pipe       stream STDIN to an object
  put        upload an object to a bucket
  quota      manage bucket quota
  rb         remove a bucket
  replicate  configure server-side bucket replication
  retention  set retention for object(s)
  rm         remove object(s)
  share      generate URL for temporary access to an object
  sql        run SQL queries on objects
  stat       show object metadata
  tag        manage tags for bucket and object(s)
  tree       list buckets and objects in a tree format
  undo       undo PUT/DELETE operations
  watch      listen for object notification events

Run 'bm <command> --help' for command-specific help.
`)
}

func printHelp() { usage(os.Stdout) }
