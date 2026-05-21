// Package preflight runs the new-cluster wizard's check catalog against a
// supplied NewClusterDraft. Each check produces a PreflightResult; the runner
// returns the full list so the UI's Preflight step can render them.
//
// The check catalog mirrors web/src/pages/wizards/new/steps/Preflight.tsx —
// keep them in sync when adding or removing checks.
package preflight

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/buckit-io/bm/internal/domain"
)

// HostConn is the per-host runner the checks call. Production wires this to
// the SSH pool (open client → ssh.Run); tests inject a fake.
type HostConn interface {
	// Run executes cmd on host and returns the captured stdout, stderr, exit code.
	Run(ctx context.Context, host domain.HostRow, cmd string) (stdout, stderr string, exit int, err error)
	// HEAD performs an HTTP HEAD against the supplied URL from bm's network
	// position (used by the `rpm` overall check).
	HEAD(ctx context.Context, url string) (statusCode int, contentLength int64, err error)
}

// Outcome is the structured return of a Check's Eval. The runner unwraps it
// into a PreflightResult.
type Outcome struct {
	// HostStatuses is set for host-scoped checks.
	HostStatuses []domain.PreflightHostStatus
	// Status + Detail are set for overall checks (and as the headline status
	// for host-scoped checks that aggregate per-host outcomes).
	Status domain.PreflightStatus
	Detail string
}

// Check is one entry in the preflight catalog.
type Check struct {
	ID       string
	Label    string
	Severity domain.PreflightSeverity
	// Eval returns the outcome for this check. Implementations should respect
	// ctx.Done() — the runner enforces an overall timeout.
	Eval func(ctx context.Context, conn HostConn, draft domain.NewClusterDraft) Outcome
}

// RunAll evaluates every check in catalog against draft. Checks run in
// parallel where possible; the per-host concurrency cap lives inside HostConn
// (typically the SSH pool's own concurrency budget). Returns one
// PreflightResult per check, in catalog order.
func RunAll(ctx context.Context, conn HostConn, draft domain.NewClusterDraft) []domain.PreflightResult {
	return RunCatalog(ctx, conn, draft, NewClusterCatalog())
}

// RunCatalog is RunAll with an explicit catalog so callers (migration) can
// drive their own check list through the same runner.
func RunCatalog(ctx context.Context, conn HostConn, draft domain.NewClusterDraft, catalog []Check) []domain.PreflightResult {
	results := make([]domain.PreflightResult, len(catalog))
	var wg sync.WaitGroup
	for i, c := range catalog {
		wg.Add(1)
		go func(i int, c Check) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			out := c.Eval(cctx, conn, draft)
			results[i] = unwrap(c, out)
		}(i, c)
	}
	wg.Wait()
	return results
}

func unwrap(c Check, out Outcome) domain.PreflightResult {
	r := domain.PreflightResult{
		ID:           c.ID,
		Label:        c.Label,
		Severity:     c.Severity,
		HostStatuses: out.HostStatuses,
		Detail:       out.Detail,
	}
	if len(out.HostStatuses) > 0 {
		r.Result = aggregate(out.HostStatuses)
	} else {
		r.Result = out.Status
	}
	if r.Result == "" {
		r.Result = domain.PreflightPass
	}
	return r
}

// aggregate folds per-host statuses into the overall result. fail wins,
// then warn, then pass.
func aggregate(hs []domain.PreflightHostStatus) domain.PreflightStatus {
	hasWarn := false
	for _, h := range hs {
		if h.Status == domain.PreflightFail {
			return domain.PreflightFail
		}
		if h.Status == domain.PreflightWarn {
			hasWarn = true
		}
	}
	if hasWarn {
		return domain.PreflightWarn
	}
	return domain.PreflightPass
}

// applicableHosts returns the host rows that have a non-empty hostname. The
// wizard's HostRow list can include placeholder rows the operator hasn't
// filled in yet — skip those.
func applicableHosts(draft domain.NewClusterDraft) []domain.HostRow {
	out := make([]domain.HostRow, 0, len(draft.Hosts))
	for _, h := range draft.Hosts {
		if h.Hostname != "" {
			out = append(out, h)
		}
	}
	return out
}

// perHost runs fn against each applicable host in parallel, fanning the
// results back into a PreflightHostStatus slice keyed by host id.
func perHost(ctx context.Context, draft domain.NewClusterDraft, fn func(ctx context.Context, h domain.HostRow) domain.PreflightHostStatus) []domain.PreflightHostStatus {
	hosts := applicableHosts(draft)
	out := make([]domain.PreflightHostStatus, len(hosts))
	var wg sync.WaitGroup
	for i, h := range hosts {
		wg.Add(1)
		go func(i int, h domain.HostRow) {
			defer wg.Done()
			out[i] = fn(ctx, h)
		}(i, h)
	}
	wg.Wait()
	// Sort by hostname so the UI always renders the same order.
	sort.Slice(out, func(i, j int) bool { return out[i].Hostname < out[j].Hostname })
	return out
}

// passStatus / failStatus are small constructors that take the noise out of
// the check evaluator bodies.
func passStatus(h domain.HostRow, msg string) domain.PreflightHostStatus {
	return domain.PreflightHostStatus{HostID: h.ID, Hostname: h.Hostname, Status: domain.PreflightPass, Message: msg}
}

func failStatus(h domain.HostRow, msg string) domain.PreflightHostStatus {
	return domain.PreflightHostStatus{HostID: h.ID, Hostname: h.Hostname, Status: domain.PreflightFail, Message: msg}
}

func warnStatus(h domain.HostRow, msg string) domain.PreflightHostStatus {
	return domain.PreflightHostStatus{HostID: h.ID, Hostname: h.Hostname, Status: domain.PreflightWarn, Message: msg}
}

func overall(status domain.PreflightStatus, detail string) Outcome {
	return Outcome{Status: status, Detail: detail}
}

// formatList renders a small set as a comma-joined string for `detail`.
func formatList(items []string) string {
	if len(items) == 0 {
		return ""
	}
	if len(items) == 1 {
		return items[0]
	}
	out := items[0]
	for _, s := range items[1:] {
		out += ", " + s
	}
	return out
}

// errSummary turns a non-nil error into a short host-status message.
func errSummary(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 80 {
		s = s[:77] + "..."
	}
	return s
}

// notImplemented is a placeholder evaluator. Real check bodies live in
// checks.go.
func notImplemented(_ context.Context, _ HostConn, _ domain.NewClusterDraft) Outcome {
	return overall(domain.PreflightSkipped, "not yet implemented")
}

// Suppress unused-warning helpers when checks are stripped via build tags
// later. Cheap no-ops.
var _ = fmt.Sprintf
