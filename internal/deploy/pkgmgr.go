package deploy

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// PackageManager abstracts the per-distro shell commands the install
// pipeline runs on the target host. Two implementations live in this
// package: rpmManager (dnf/yum + .rpm) and debManager (apt-get + .deb).
//
// All methods return raw shell snippets — callers wrap them in SudoWrap
// or runHostStep as appropriate.
type PackageManager interface {
	// Kind matches BuckitArtifact.Kind: "rpm" or "deb".
	Kind() string

	// LocalFile is the absolute path on the host where the package is
	// staged after download (e.g. /tmp/buckit.rpm or /tmp/buckit.deb).
	LocalFile() string

	// DownloadCommand fetches url to LocalFile via curl.
	DownloadCommand(url string) string

	// VerifyChecksumCommand reads the expected sha256 on stdin and
	// verifies the local file. Returns exit 0 on match.
	VerifyChecksumCommand(expectedSHA256 string) string

	// InspectScript is the shell script that emits three lines:
	//   installed=<EVR or empty if not installed>
	//   candidate=<EVR of the local file>
	//   cmp=<-1 | 0 | 1>
	// where cmp follows rpm.vercmp semantics: -1 if installed < candidate,
	// 0 if equal (or no install), 1 if installed > candidate.
	InspectScript() string

	// InstallCommand returns the install command for one of the actions.
	// Downgrade is never expected here — callers reject it before invoking.
	InstallCommand(action InstallAction) string
}

// InstallAction names the three install verbs the pipeline picks between
// after inspecting the host's currently-installed package against the
// candidate that was just downloaded. Downgrade is included for
// completeness — callers must reject it before calling InstallCommand.
type InstallAction string

const (
	InstallActionFresh     InstallAction = "fresh"
	InstallActionReinstall InstallAction = "reinstall"
	InstallActionUpgrade   InstallAction = "upgrade"
	InstallActionDowngrade InstallAction = "downgrade"
)

const (
	rpmLocalPath = "/tmp/buckit.rpm"
	debLocalPath = "/tmp/buckit.deb"
)

// rpmManager drives RHEL/Rocky/Alma/CentOS hosts. The verb is "dnf" by
// default and "yum" on EL7-era hosts that lack dnf. Both share identical
// `install -y` / `upgrade -y` / `reinstall -y` invocations.
type rpmManager struct {
	// verb is "dnf" or "yum". Default (zero value) is treated as "dnf".
	verb string
}

// NewRPMManager returns an rpmManager with the given verb. Accepted
// values are "dnf" and "yum"; an unknown verb is replaced with "dnf"
// since the preflight rejects hosts that ship neither.
func NewRPMManager(verb string) rpmManager {
	v := strings.TrimSpace(verb)
	if v != "yum" {
		v = "dnf"
	}
	return rpmManager{verb: v}
}

func (m rpmManager) Kind() string      { return "rpm" }
func (m rpmManager) LocalFile() string { return rpmLocalPath }

func (m rpmManager) verbOrDefault() string {
	if m.verb == "" {
		return "dnf"
	}
	return m.verb
}

func (m rpmManager) DownloadCommand(url string) string {
	return fmt.Sprintf("curl -fSL -o %s %s", rpmLocalPath, ShellEscape(url))
}

func (m rpmManager) VerifyChecksumCommand(expectedSHA256 string) string {
	return checksumVerifyCommand(expectedSHA256, rpmLocalPath)
}

// InspectScript emits installed=/candidate=/cmp= for the buckit
// package using rpm + rpm.vercmp. The `rpm -q --quiet` guard matters:
// when the package is missing, `rpm -q --qf '<fmt>' buckit` prints
// "package buckit is not installed" to *stdout* (not stderr) and
// exits 1, so a naive `$(... || true)` capture would set installed to
// that error text and ParseInspectOutput would then misclassify the
// host as "something is installed."
func (m rpmManager) InspectScript() string {
	return `installed=""
if rpm -q --quiet buckit 2>/dev/null; then
  installed=$(rpm -q --qf '%{EPOCHNUM}:%{VERSION}-%{RELEASE}.%{ARCH}\n' buckit 2>/dev/null)
fi
candidate=$(rpm -qp --qf '%{EPOCHNUM}:%{VERSION}-%{RELEASE}.%{ARCH}\n' /tmp/buckit.rpm)
cmp=0
if [ -n "$installed" ] && [ "$installed" != "$candidate" ]; then
  ivr=$(rpm -q --qf '%{VERSION}-%{RELEASE}' buckit 2>/dev/null)
  cvr=$(rpm -qp --qf '%{VERSION}-%{RELEASE}' /tmp/buckit.rpm)
  cmp=$(rpm --eval "%{lua: print(rpm.vercmp('$ivr', '$cvr'))}" 2>/dev/null)
fi
printf 'installed=%s\ncandidate=%s\ncmp=%s\n' "$installed" "$candidate" "$cmp"`
}

func (m rpmManager) InstallCommand(action InstallAction) string {
	verb := m.verbOrDefault()
	switch action {
	case InstallActionFresh:
		return verb + " install -y " + rpmLocalPath
	case InstallActionUpgrade:
		return verb + " upgrade -y " + rpmLocalPath
	case InstallActionReinstall:
		return verb + " reinstall -y " + rpmLocalPath
	case InstallActionDowngrade:
		// Caller should have rejected. Returning a refusing command
		// keeps a fatal error path even if the guard ever regresses.
		return "echo 'downgrade unsupported' >&2; exit 1"
	default:
		return verb + " install -y " + rpmLocalPath
	}
}

// debManager drives Debian/Ubuntu hosts via apt-get + dpkg-query.
type debManager struct{}

// NewDebManager returns the default debManager. The struct is stateless;
// the constructor exists for symmetry with NewRPMManager.
func NewDebManager() debManager { return debManager{} }

func (m debManager) Kind() string      { return "deb" }
func (m debManager) LocalFile() string { return debLocalPath }

func (m debManager) DownloadCommand(url string) string {
	return fmt.Sprintf("curl -fSL -o %s %s", debLocalPath, ShellEscape(url))
}

func (m debManager) VerifyChecksumCommand(expectedSHA256 string) string {
	return checksumVerifyCommand(expectedSHA256, debLocalPath)
}

// InspectScript emits installed/candidate/cmp using dpkg-query +
// dpkg-deb + `dpkg --compare-versions`. Output shape matches
// rpmManager so the Go-side parser stays generic.
func (m debManager) InspectScript() string {
	return `installed=$(dpkg-query -W -f='${Version}' buckit 2>/dev/null || true)
candidate=$(dpkg-deb -f /tmp/buckit.deb Version)
cmp=0
if [ -n "$installed" ] && [ "$installed" != "$candidate" ]; then
  if dpkg --compare-versions "$installed" lt "$candidate"; then
    cmp=-1
  elif dpkg --compare-versions "$installed" gt "$candidate"; then
    cmp=1
  fi
fi
printf 'installed=%s\ncandidate=%s\ncmp=%s\n' "$installed" "$candidate" "$cmp"`
}

func (m debManager) InstallCommand(action InstallAction) string {
	switch action {
	case InstallActionFresh, InstallActionUpgrade:
		// apt detects direction; same command for fresh and upgrade.
		return "apt-get install -y " + debLocalPath
	case InstallActionReinstall:
		return "apt-get install -y --reinstall " + debLocalPath
	case InstallActionDowngrade:
		return "echo 'downgrade unsupported' >&2; exit 1"
	default:
		return "apt-get install -y " + debLocalPath
	}
}

// checksumVerifyCommand is the shared sha256 verifier used by both
// managers. It mirrors what VerifyRPMChecksumCommand used to emit
// inline; centralizing it here means the deb path doesn't accidentally
// diverge.
func checksumVerifyCommand(expectedSHA256, localPath string) string {
	line := fmt.Sprintf("%s  %s\\n", strings.TrimSpace(expectedSHA256), localPath)
	quotedLine := ShellEscape(line)
	return strings.Join([]string{
		"if command -v sha256sum >/dev/null 2>&1; then",
		"printf " + quotedLine + " | sha256sum -c -;",
		"elif command -v shasum >/dev/null 2>&1; then",
		"printf " + quotedLine + " | shasum -a 256 -c -;",
		"else",
		"echo 'sha256 tool not found' >&2;",
		"exit 1;",
		"fi",
	}, " ")
}

// ParseInspectOutput consumes the three lines emitted by InspectScript
// and decides the InstallAction. It is package-manager-agnostic — the
// shell side normalizes the comparison so this Go parser never has to
// know whether rpmvercmp or `dpkg --compare-versions` produced cmp.
func ParseInspectOutput(out string) (InstallAction, error) {
	var installed, candidate, cmpStr string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "installed="):
			installed = strings.TrimSpace(strings.TrimPrefix(line, "installed="))
		case strings.HasPrefix(line, "candidate="):
			candidate = strings.TrimSpace(strings.TrimPrefix(line, "candidate="))
		case strings.HasPrefix(line, "cmp="):
			cmpStr = strings.TrimSpace(strings.TrimPrefix(line, "cmp="))
		}
	}
	if candidate == "" {
		return "", errors.New("empty candidate package identity")
	}
	if installed == "" {
		// Nothing on disk → caller must use the fresh-install verb.
		// `dnf upgrade` refuses to install a package that isn't
		// already present ("No packages marked for upgrade"), so this
		// distinction matters even though semantically "upgrading from
		// nothing" sounds equivalent.
		return InstallActionFresh, nil
	}
	if installed == candidate {
		return InstallActionReinstall, nil
	}
	switch cmpStr {
	case "-1":
		return InstallActionUpgrade, nil
	case "1":
		return InstallActionDowngrade, nil
	case "0":
		// EVR strings differed but V-R compared equal — only epoch or
		// arch could differ. Same-version reinstall is the safe read.
		return InstallActionReinstall, nil
	default:
		return "", fmt.Errorf("unexpected vercmp result %q for installed=%s candidate=%s", cmpStr, installed, candidate)
	}
}

// DetectPackageManager probes a host for dnf / yum / apt-get and
// returns the matching PackageManager. Preference order: dnf > yum >
// apt-get, preserving the bias toward dnf on RHEL hosts that may also
// have apt-get installed as a side effect of something else.
//
// runShell takes a single shell command and returns its stdout (the
// caller is responsible for sudo / ssh / context plumbing). A non-zero
// exit is signalled by err != nil — DetectPackageManager treats that as
// "not found" and moves on.
func DetectPackageManager(
	ctx context.Context,
	runShell func(context.Context, string) (string, error),
) (PackageManager, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out, err := runShell(ctx, "command -v dnf 2>/dev/null; command -v yum 2>/dev/null; command -v apt-get 2>/dev/null; true")
	if err != nil {
		return nil, fmt.Errorf("probe package manager: %w", err)
	}
	hasDNF, hasYum, hasApt := false, false, false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasSuffix(line, "/dnf") || line == "dnf":
			hasDNF = true
		case strings.HasSuffix(line, "/yum") || line == "yum":
			hasYum = true
		case strings.HasSuffix(line, "/apt-get") || line == "apt-get":
			hasApt = true
		}
	}
	switch {
	case hasDNF:
		return NewRPMManager("dnf"), nil
	case hasYum:
		return NewRPMManager("yum"), nil
	case hasApt:
		return NewDebManager(), nil
	default:
		return nil, errors.New("no supported package manager found (looked for dnf, yum, apt-get)")
	}
}
