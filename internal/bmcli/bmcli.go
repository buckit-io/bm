// Package bmcli bridges bm's main entry point to the forked buckit-io/bm-cli.
//
// All mc-style verbs (cp, ls, mb, alias, admin *, …) come from the fork
// unchanged. bm-native verbs (web, version) intercept first; everything else
// is handed off to bmcli.Delegate, which fixes up os.Args so the fork sees
// the bm config directory instead of `~/.mc/`.
package bmcli

import (
	"strings"

	bmcli "github.com/buckit-io/bm-cli/cmd"
)

// Delegate calls into the forked bm-cli's Main with args rewritten so the
// `--config-dir` global flag points at configDir. configDir is typically the
// bm config dir (e.g. ~/.config/bm); bm-cli will look for `config.json` under
// it.
func Delegate(args []string, configDir string) error {
	return bmcli.Main(withConfigDir(args, configDir))
}

func withConfigDir(args []string, configDir string) []string {
	if configDir == "" || hasConfigDirFlag(args) {
		return args
	}
	// Insert the flag immediately after the program name (args[0]) so it
	// is parsed as a global flag, before the subcommand name.
	out := make([]string, 0, len(args)+1)
	if len(args) > 0 {
		out = append(out, args[0])
	}
	out = append(out, "--config-dir="+configDir)
	if len(args) > 1 {
		out = append(out, args[1:]...)
	}
	return out
}

func hasConfigDirFlag(args []string) bool {
	for _, a := range args {
		if a == "--config-dir" || strings.HasPrefix(a, "--config-dir=") || a == "-config-dir" || strings.HasPrefix(a, "-config-dir=") {
			return true
		}
	}
	return false
}
