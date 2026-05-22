package admin

import (
	"time"

	madmin "github.com/buckit-io/madmin-go/v3"

	"github.com/buckit-io/bm/internal/domain"
)

// mapServerInfo folds madmin.InfoMessage into domain.ServerInfo. Skips fields
// bm doesn't use today (Backend internals, Mem/GC stats, ILM bookkeeping).
func mapServerInfo(msg *madmin.InfoMessage) *domain.ServerInfo {
	if msg == nil {
		return nil
	}
	out := &domain.ServerInfo{
		Mode:    msg.Mode,
		Parity:  msg.StandardParity(),
		Servers: make([]domain.ServerInfoServer, 0, len(msg.Servers)),
		Pools:   len(msg.Pools),
	}

	// Version comes from any server entry; servers should agree.
	if len(msg.Servers) > 0 {
		out.Version = msg.Servers[0].Version
	}

	for _, s := range msg.Servers {
		out.Servers = append(out.Servers, mapServer(s))
	}

	// Roll up raw / used / usable from the drives if the response doesn't
	// surface them at the top level (madmin's InfoMessage doesn't carry them
	// directly — Usage.Size is sized differently across versions).
	var raw, used int64
	for _, server := range out.Servers {
		for _, d := range server.Drives {
			if d.IsBoot {
				continue
			}
			raw += d.SizeBytes
			used += d.UsedBytes
		}
	}
	out.Raw = raw
	out.Used = used
	if out.Parity > 0 && len(out.Servers) > 0 {
		drives := 0
		for _, s := range out.Servers {
			for _, d := range s.Drives {
				if !d.IsBoot {
					drives++
				}
			}
		}
		if drives > 0 {
			usable := raw - (int64(out.Parity) * (raw / int64(drives)))
			out.Usable = usable
		}
	}
	return out
}

func mapServer(s madmin.ServerProperties) domain.ServerInfoServer {
	out := domain.ServerInfoServer{
		Endpoint:   s.Endpoint,
		State:      mapNodeState(s.State),
		PoolNumber: s.PoolNumber,
		Version:    s.Version,
		OS:         s.OS,
		Arch:       s.Arch,
		Drives:     make([]domain.ServerInfoDrive, 0, len(s.Disks)),
	}
	if s.Uptime > 0 {
		uptime := float64(s.Uptime)
		out.Uptime = &uptime
	}
	for _, d := range s.Disks {
		out.Drives = append(out.Drives, mapDrive(d))
	}
	return out
}

func mapDrive(d madmin.Disk) domain.ServerInfoDrive {
	return domain.ServerInfoDrive{
		Mount:     d.DrivePath,
		Device:    d.Endpoint,
		SizeBytes: int64(d.TotalSpace),
		UsedBytes: int64(d.UsedSpace),
		State:     mapDriveState(d.State, d.Healing),
		IsBoot:    d.RootDisk,
	}
}

func mapNodeState(state string) domain.NodeState {
	switch state {
	case "online", "ok":
		return domain.NodeOnline
	case "offline", "down":
		return domain.NodeOffline
	case "degraded":
		return domain.NodeDegraded
	default:
		return domain.NodeUnknown
	}
}

func mapDriveState(state string, healing bool) domain.DriveState {
	if healing {
		return domain.DriveHealing
	}
	switch state {
	case "ok", "online":
		return domain.DriveReady
	case "healing":
		return domain.DriveHealing
	default:
		return domain.DriveFailed
	}
}

// freshness is a marker stamped on ServerInfo when read; the caller's
// time.Now is preferred over the madmin response so cluster_admin lookups
// don't get out-of-sync timestamps across distributed clusters.
func nowUTC() time.Time { return time.Now().UTC() }
