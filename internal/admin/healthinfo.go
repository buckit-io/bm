package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	madmin "github.com/buckit-io/madmin-go/v3"

	"github.com/buckit-io/bm/internal/domain"
)

// healthInfoDeadline bounds the upstream-server-side collection window. The
// endpoint streams chunks as each datatype's collector finishes, so the
// deadline doubles as a per-datatype budget. 30s is enough for cpu/os/mem/net
// on a healthy cluster while still failing fast when a node is wedged.
const healthInfoDeadline = 30 * time.Second

// HealthInfo fetches /minio/admin/v3/healthinfo, restricting the request to
// the syscpu/sysosinfo/sysmem/sysnet datatypes that populate the node-detail
// System + Hardware cards. The response is streamed: madmin emits one JSON
// frame per datatype as the upstream collector finishes, and json.Decoder
// accumulates them into a single madmin.HealthInfo struct (each frame only
// fills the slice it owns).
//
// Returns the folded domain.HealthInfo. A nil return with a non-nil error
// signals the fetch never produced any usable frames (network failure,
// unsupported server version, etc.).
func (c *Client) HealthInfo(ctx context.Context) (*domain.HealthInfo, error) {
	if c == nil || c.adm == nil {
		return nil, errors.New("admin: nil client")
	}
	types := []madmin.HealthDataType{
		madmin.HealthDataTypeSysCPU,
		madmin.HealthDataTypeSysOsInfo,
		madmin.HealthDataTypeSysMem,
		madmin.HealthDataTypeSysNet,
	}
	resp, version, err := c.adm.ServerHealthInfo(ctx, types, healthInfoDeadline, "")
	if err != nil {
		return nil, classifyError(err)
	}
	defer resp.Body.Close()
	if version != madmin.HealthInfoVersion {
		return nil, &Error{Kind: ErrOther, Cause: errors.New("admin: unsupported healthinfo version " + version)}
	}

	var info madmin.HealthInfo
	dec := json.NewDecoder(resp.Body)
	for {
		if err := dec.Decode(&info); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// A mid-stream decode error usually means the upstream cut the
			// connection after emitting a partial frame; surface what we
			// already collected rather than failing the whole call.
			break
		}
	}
	return mapHealthInfo(&info), nil
}
