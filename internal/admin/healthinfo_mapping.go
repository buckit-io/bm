package admin

import (
	"sort"

	madmin "github.com/buckit-io/madmin-go/v3"

	"github.com/buckit-io/bm/internal/domain"
)

// mapHealthInfo folds madmin.HealthInfo into the domain shape. Pivots from
// madmin's per-datatype slices (one entry per host per datatype) to a
// per-host bundle keyed by addr.
func mapHealthInfo(info *madmin.HealthInfo) *domain.HealthInfo {
	if info == nil {
		return nil
	}
	out := &domain.HealthInfo{
		Version:   info.Version,
		Timestamp: info.TimeStamp,
		Error:     info.Error,
	}

	byAddr := map[string]*domain.HostHealthInfo{}
	getHost := func(addr string) *domain.HostHealthInfo {
		if h, ok := byAddr[addr]; ok {
			return h
		}
		h := &domain.HostHealthInfo{Addr: addr}
		byAddr[addr] = h
		return h
	}

	for _, os := range info.Sys.OSInfo {
		h := getHost(os.Addr)
		if os.Error != "" {
			h.Errors = append(h.Errors, "osinfo: "+os.Error)
			continue
		}
		h.OS = &domain.HostOSInfo{
			KernelVersion:   os.Info.KernelVersion,
			KernelArch:      os.Info.KernelArch,
			Platform:        os.Info.Platform,
			PlatformFamily:  os.Info.PlatformFamily,
			PlatformVersion: os.Info.PlatformVersion,
			Hostname:        os.Info.Hostname,
			Uptime:          os.Info.Uptime,
			BootTime:        os.Info.BootTime,
		}
	}

	for _, mem := range info.Sys.MemInfo {
		h := getHost(mem.Addr)
		if mem.Error != "" {
			h.Errors = append(h.Errors, "meminfo: "+mem.Error)
			continue
		}
		h.Mem = &domain.HostMemInfo{
			Total:     mem.Total,
			Used:      mem.Used,
			Free:      mem.Free,
			Available: mem.Available,
			SwapTotal: mem.SwapSpaceTotal,
		}
	}

	for _, cpus := range info.Sys.CPUInfo {
		h := getHost(cpus.Addr)
		if cpus.Error != "" {
			h.Errors = append(h.Errors, "cpus: "+cpus.Error)
			continue
		}
		for _, cpu := range cpus.CPUs {
			h.CPUs = append(h.CPUs, domain.HostCPUInfo{
				ModelName:  cpu.ModelName,
				VendorID:   cpu.VendorID,
				Mhz:        cpu.Mhz,
				Cores:      cpu.Cores,
				PhysicalID: cpu.PhysicalID,
			})
		}
	}

	for _, nic := range info.Sys.NetInfo {
		h := getHost(nic.Addr)
		if nic.Error != "" {
			h.Errors = append(h.Errors, "netinfo: "+nic.Error)
			continue
		}
		if nic.Interface == "" {
			continue
		}
		h.NICs = append(h.NICs, domain.HostNICInfo{
			Interface:       nic.Interface,
			Driver:          nic.Driver,
			FirmwareVersion: nic.FirmwareVersion,
		})
	}

	out.Hosts = make([]domain.HostHealthInfo, 0, len(byAddr))
	for _, h := range byAddr {
		out.Hosts = append(out.Hosts, *h)
	}
	sort.Slice(out.Hosts, func(i, j int) bool { return out.Hosts[i].Addr < out.Hosts[j].Addr })
	return out
}
