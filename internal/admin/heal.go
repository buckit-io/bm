package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	madmin "github.com/buckit-io/madmin-go/v3"
)

// HealStart kicks off a heal task. Returns the clientToken the caller passes
// to HealStatus on each poll iteration, plus the time the task started.
//
// bucket="" + prefix="" heals the whole cluster. recursive=true heals all
// objects under the prefix.
func (c *Client) HealStart(ctx context.Context, bucket, prefix string, recursive bool) (token string, startedAt time.Time, err error) {
	if c == nil || c.adm == nil {
		return "", time.Time{}, errors.New("admin: nil client")
	}
	opts := madmin.HealOpts{
		Recursive: recursive,
		ScanMode:  madmin.HealNormalScan,
	}
	start, _, err := c.adm.Heal(ctx, bucket, prefix, opts, "", true, false)
	if err != nil {
		return "", time.Time{}, classifyError(err)
	}
	return start.ClientToken, start.StartTime, nil
}

// HealStatus polls the in-flight heal task for incremental results. Returns
// the items produced since the last call; an empty slice + completed=true
// means the heal finished. The status struct's Summary field surfaces
// "finished" / "running" / "stopped" for the executor's terminal check.
func (c *Client) HealStatus(ctx context.Context, bucket, prefix, token string) (status HealStatus, err error) {
	if c == nil || c.adm == nil {
		return HealStatus{}, errors.New("admin: nil client")
	}
	if token == "" {
		return HealStatus{}, errors.New("admin: heal token required")
	}
	_, ts, err := c.adm.Heal(ctx, bucket, prefix, madmin.HealOpts{}, token, false, false)
	if err != nil {
		return HealStatus{}, classifyError(err)
	}
	out := HealStatus{
		Summary:       ts.Summary,
		StartedAt:     ts.StartTime,
		FailureDetail: ts.FailureDetail,
	}
	for _, it := range ts.Items {
		out.Items = append(out.Items, HealItem{
			Type:       string(it.Type),
			Bucket:     it.Bucket,
			Object:     it.Object,
			ObjectSize: it.ObjectSize,
			Detail:     it.Detail,
		})
	}
	return out, nil
}

// HealStop tells the cluster to abort the in-flight heal. Used by the
// orchestrator's cancel path.
func (c *Client) HealStop(ctx context.Context, bucket, prefix, token string) error {
	if c == nil || c.adm == nil {
		return errors.New("admin: nil client")
	}
	if _, _, err := c.adm.Heal(ctx, bucket, prefix, madmin.HealOpts{}, token, false, true); err != nil {
		return classifyError(err)
	}
	return nil
}

// HealStatus is the domain-typed slice of madmin.HealTaskStatus the
// executor consumes. Hides the upstream's HealResultItem complexity behind
// HealItem.
type HealStatus struct {
	Summary       string     `json:"summary"` // "running" | "finished" | "stopped"
	FailureDetail string     `json:"failureDetail,omitempty"`
	StartedAt     time.Time  `json:"startedAt"`
	Items         []HealItem `json:"items,omitempty"`
}

// IsTerminal reports whether the heal has finished or been stopped.
func (s HealStatus) IsTerminal() bool {
	return s.Summary == "finished" || s.Summary == "stopped"
}

// HealItem is a per-object heal result. The executor surfaces these as
// OpEvents (coalesced — emit summary every 100 items).
type HealItem struct {
	Type       string `json:"type"`
	Bucket     string `json:"bucket"`
	Object     string `json:"object,omitempty"`
	ObjectSize int64  `json:"objectSize,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// Format is a short single-line description used in OpEvent text.
func (h HealItem) Format() string {
	if h.Object == "" {
		return fmt.Sprintf("[%s] %s", h.Type, h.Bucket)
	}
	return fmt.Sprintf("[%s] %s/%s", h.Type, h.Bucket, h.Object)
}
