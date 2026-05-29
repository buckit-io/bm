// Package deploy implements the per-host SSH work the new-cluster wizard
// needs: rich discovery (RichProbe) for the Discover step, the supported
// versions catalog for the Basics step, and the install pipeline (M6).
package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/buckit-io/bm/internal/domain"
	bmssh "github.com/buckit-io/bm/internal/ssh"
)

// RichProbe gathers the per-host facts the wizard's Discover step renders.
// Reuses the supplied client; callers wire the ssh.Pool so connections
// amortize across the preflight pass that runs right after.
//
// Failure modes are reported on the returned struct rather than as errors so
// the wizard can surface a per-host "discovery failed" tile without the whole
// request failing.
func RichProbe(ctx context.Context, client *ssh.Client) domain.WizardDiscoveryResult {
	out := domain.WizardDiscoveryResult{State: domain.WizardDiscoveryRunning}
	if client == nil {
		out.State = domain.WizardDiscoveryFailed
		out.Error = "no ssh client"
		return out
	}

	if v, ok := runOK(ctx, client, "uname -r"); ok {
		out.Kernel = strings.TrimSpace(v)
	}
	if v, ok := runOK(ctx, client, "uname -m"); ok {
		out.Arch = normalizeArch(strings.TrimSpace(v))
	}
	if v, ok := runOK(ctx, client, "cat /etc/os-release"); ok {
		out.OS = parseOSRelease(v)
	}
	if v, ok := runOK(ctx, client, "nproc"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			out.Cores = &n
		}
	}
	if v, ok := runOK(ctx, client, "cat /proc/meminfo"); ok {
		if g := parseMemTotalGiB(v); g > 0 {
			out.RamGiB = &g
		}
	}

	mountSizes := map[string]int64{}
	if v, ok := runOK(ctx, client, "df -B1 --output=target,size"); ok {
		mountSizes = parseMountSizes(v)
	}
	if v, ok := runOK(ctx, client, "cat /proc/self/mountinfo"); ok {
		if drives := parseMountInfoDrives(v, mountSizes); len(drives) > 0 {
			out.Drives = drives
		}
	}
	if v, ok := runOK(ctx, client, "ip -j link"); ok {
		if nic := parsePrimaryNIC(v); nic != "" {
			out.NIC = nic
		}
	}

	// Existing service detection. The systemctl status exit code is 3 when the
	// unit doesn't exist and 0/3 when it does — rely on the unit-name presence
	// in the output rather than the exit code.
	if v, ok := runOK(ctx, client, "systemctl status buckit.service --no-pager -l"); ok && strings.Contains(v, "Loaded:") {
		out.ExistingService = "buckit"
	} else if v, ok := runOK(ctx, client, "systemctl status minio.service --no-pager -l"); ok && strings.Contains(v, "Loaded:") {
		out.ExistingService = "minio"
	}

	// Sudo check — only meaningful when not running as root.
	if v, ok := runOK(ctx, client, "id -u"); ok && strings.TrimSpace(v) != "0" {
		_, sudoOK := runOK(ctx, client, "sudo -n true")
		out.SudoOk = &sudoOK
	}

	out.State = domain.WizardDiscoveryDone
	return out
}

// runOK is a thin wrapper around ssh.Run that returns (stdout, true) on exit
// code 0 and ("", false) otherwise. Hides the bookkeeping noise in RichProbe.
func runOK(ctx context.Context, c *ssh.Client, cmd string) (string, bool) {
	r, err := bmssh.Run(ctx, c, cmd)
	if err != nil || r.ExitCode != 0 {
		return "", false
	}
	return r.Stdout, true
}

func normalizeArch(raw string) string {
	switch raw {
	case "x86_64":
		return "amd64"
	case "aarch64":
		return "arm64"
	case "armv7l", "armhf":
		return "arm"
	default:
		return raw
	}
}

func parseOSRelease(body string) string {
	fields := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		v := strings.Trim(strings.TrimSpace(line[eq+1:]), `"`)
		fields[line[:eq]] = v
	}
	if v := fields["PRETTY_NAME"]; v != "" {
		return v
	}
	if name := fields["NAME"]; name != "" {
		if ver := fields["VERSION"]; ver != "" {
			return name + " " + ver
		}
		return name
	}
	return ""
}

func parseMemTotalGiB(meminfo string) int {
	for _, line := range strings.Split(meminfo, "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		// MemTotal is in kB; round to nearest GiB (1 GiB ≈ 1_048_576 KiB).
		return int((kb + 524288) / 1048576)
	}
	return 0
}

func parseMountInfoDrives(mountInfo string, mountSizes map[string]int64) []domain.DiscoveredDrive {
	entries := parseMountInfo(mountInfo)
	out := make([]domain.DiscoveredDrive, 0, len(entries))
	for _, e := range entries {
		mount := normalizeMountPath(e.MountPoint)
		if mount == "" {
			continue
		}
		if isPseudoMountType(e.FSType) {
			continue
		}
		out = append(out, domain.DiscoveredDrive{
			Device:    strings.TrimSpace(e.Source),
			Mount:     mount,
			SizeBytes: mountSizes[mount],
			FsType:    e.FSType,
			IsBoot:    mount == "/" || mount == "/boot" || strings.HasPrefix(mount, "/boot/"),
			IsSystem:  isSystemMountPath(mount),
		})
	}
	return out
}

func isPseudoMountType(fsType string) bool {
	switch fsType {
	case "proc", "sysfs", "devtmpfs", "devpts", "tmpfs", "cgroup", "cgroup2", "pstore", "securityfs", "debugfs", "tracefs", "bpf", "mqueue", "hugetlbfs", "fusectl", "configfs", "overlay", "nsfs", "autofs", "rpc_pipefs":
		return true
	default:
		return false
	}
}

func isSystemMountPath(mount string) bool {
	mount = normalizeMountPath(mount)
	if mount == "" {
		return false
	}
	if mount == "/" {
		return true
	}
	for _, prefix := range []string{"/boot", "/home", "/var", "/etc", "/tmp", "/usr", "/root"} {
		if mount == prefix || strings.HasPrefix(mount, prefix+"/") {
			return true
		}
	}
	return false
}

func normalizeMountPath(mount string) string {
	mount = strings.TrimSpace(mount)
	if mount == "" || mount == "/" {
		return mount
	}
	return strings.TrimRight(mount, "/")
}

// ipLinkOutput is the JSON shape of `ip -j link`.
type ipLinkOutput []struct {
	IfName    string `json:"ifname"`
	LinkType  string `json:"link_type"`
	Operstate string `json:"operstate"`
	Address   string `json:"address"`
}

func parsePrimaryNIC(jsonBody string) string {
	var entries ipLinkOutput
	if err := json.Unmarshal([]byte(jsonBody), &entries); err != nil {
		return ""
	}
	// First "up" ether interface that isn't loopback.
	for _, e := range entries {
		if e.IfName == "lo" {
			continue
		}
		if e.LinkType == "ether" && strings.ToUpper(e.Operstate) == "UP" {
			return e.IfName
		}
	}
	for _, e := range entries {
		if e.IfName != "lo" && e.LinkType == "ether" {
			return e.IfName
		}
	}
	return ""
}

// formatPair is a small helper used by other deploy files.
func formatPair(k, v string) string { return fmt.Sprintf("%s=%s", k, v) }
