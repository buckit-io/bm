package deploy

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"

	bmssh "github.com/buckit-io/bm/internal/ssh"
)

// UnitProps captures the subset of `systemctl show <unit>` properties the
// migration cutover needs to align buckit.service with the unit it replaces.
// Fields mirror systemd directive names (with their unit-file semantics).
type UnitProps struct {
	// User and Group are systemd's effective values. Empty means the unit
	// did not set the directive — systemd's documented fallback is root,
	// which the caller should apply when comparing two units.
	User  string
	Group string

	// EnvironmentFiles lists the EnvironmentFile= paths in unit-file order.
	// Leading `-` ignore-errors markers and trailing `(ignore_errors=…)`
	// annotations are stripped so each entry is a clean path. Empty when
	// the unit declares no EnvironmentFile=.
	EnvironmentFiles []string
}

// LoadUnitProps runs `systemctl show <unit> -p User -p Group
// -p EnvironmentFiles -p EnvironmentFile` over client and returns the
// parsed properties. Wraps the command in sudo when creds require it
// (some distros restrict systemctl to root).
//
// We ask for both `EnvironmentFiles` (plural — modern systemd's dbus
// property name) and `EnvironmentFile` (singular — what older systemd
// emits). Whichever one the local systemd recognizes is what shows up
// in stdout; unknown property names are silently dropped. The parser
// accepts either spelling.
//
// Returns an error only on transport / non-zero exit failure. A missing
// unit is *not* an error here — systemd's `show` prints empty values for
// units it doesn't know about and exits 0; callers that need a presence
// check should use LoadState instead.
func LoadUnitProps(ctx context.Context, client *ssh.Client, creds bmssh.Resolved, unit string) (UnitProps, error) {
	cmd := SudoWrap(creds, fmt.Sprintf("systemctl show %s -p User -p Group -p EnvironmentFiles -p EnvironmentFile", ShellEscape(unit)))
	r, err := bmssh.Run(ctx, client, cmd)
	if err != nil {
		return UnitProps{}, err
	}
	if r.ExitCode != 0 {
		stderr := strings.TrimSpace(r.Stderr)
		if stderr == "" {
			stderr = fmt.Sprintf("exit %d", r.ExitCode)
		}
		return UnitProps{}, fmt.Errorf("systemctl show %s: %s", unit, stderr)
	}
	return ParseUnitProps(r.Stdout), nil
}

// ParseUnitProps parses the stdout of `systemctl show -p User -p Group
// -p EnvironmentFile`. Exported so callers that issue the command through
// a transport other than *ssh.Client (e.g., the migration preflight's
// HostConn) can reuse the parser. Handles pre-v240 and modern systemd
// output (the format is stable for these three properties).
func ParseUnitProps(out string) UnitProps {
	var props UnitProps
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "User="):
			props.User = strings.TrimSpace(strings.TrimPrefix(line, "User="))
		case strings.HasPrefix(line, "Group="):
			props.Group = strings.TrimSpace(strings.TrimPrefix(line, "Group="))
		case strings.HasPrefix(line, "EnvironmentFiles="):
			// Modern systemd (≥ ~244) names this property in the plural.
			v := strings.TrimSpace(strings.TrimPrefix(line, "EnvironmentFiles="))
			if path := parseEnvironmentFileValue(v); path != "" {
				props.EnvironmentFiles = append(props.EnvironmentFiles, path)
			}
		case strings.HasPrefix(line, "EnvironmentFile="):
			// Older systemd echoed back the singular form. Kept for
			// compatibility with hosts we haven't reproduced against.
			v := strings.TrimSpace(strings.TrimPrefix(line, "EnvironmentFile="))
			if path := parseEnvironmentFileValue(v); path != "" {
				props.EnvironmentFiles = append(props.EnvironmentFiles, path)
			}
		}
	}
	return props
}

// parseEnvironmentFileValue extracts the path from one `systemctl show`
// EnvironmentFile= line value. Strips the trailing `(ignore_errors=…)` /
// `(...)` annotation and the leading `-` ignore-errors marker (older
// systemd versions can echo the marker back). Returns "" for empty input
// so callers can use it as an additive filter.
func parseEnvironmentFileValue(v string) string {
	if i := strings.LastIndex(v, " ("); i >= 0 {
		v = v[:i]
	}
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "-")
	return strings.TrimSpace(v)
}
