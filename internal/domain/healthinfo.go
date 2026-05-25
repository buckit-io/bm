package domain

import "time"

// HealthInfo is the slice of madmin's /healthinfo response that bm consumes.
// Only the per-host system + hardware facts the node-detail UI needs are
// folded in — process/service/config blobs are deliberately omitted.
type HealthInfo struct {
	Version   string           `json:"version,omitempty"`
	Timestamp time.Time        `json:"timestamp"`
	Hosts     []HostHealthInfo `json:"hosts"`
	Error     string           `json:"error,omitempty"`
}

// HostHealthInfo is the per-host bundle. Addr is the upstream's reported
// endpoint (host:port), used to join against domain.Node by hostname.
type HostHealthInfo struct {
	Addr   string        `json:"addr"`
	OS     *HostOSInfo   `json:"os,omitempty"`
	Mem    *HostMemInfo  `json:"mem,omitempty"`
	CPUs   []HostCPUInfo `json:"cpus,omitempty"`
	NICs   []HostNICInfo `json:"nics,omitempty"`
	Errors []string      `json:"errors,omitempty"`
}

// HostOSInfo carries the kernel/distro facts surfaced in the System card.
type HostOSInfo struct {
	KernelVersion   string `json:"kernelVersion,omitempty"`
	KernelArch      string `json:"kernelArch,omitempty"`
	Platform        string `json:"platform,omitempty"`
	PlatformFamily  string `json:"platformFamily,omitempty"`
	PlatformVersion string `json:"platformVersion,omitempty"`
	Hostname        string `json:"hostname,omitempty"`
	Uptime          uint64 `json:"uptime,omitempty"`
	BootTime        uint64 `json:"bootTime,omitempty"`
}

// HostMemInfo is physical-RAM totals; not Go heap stats.
type HostMemInfo struct {
	Total     uint64 `json:"total,omitempty"`
	Used      uint64 `json:"used,omitempty"`
	Free      uint64 `json:"free,omitempty"`
	Available uint64 `json:"available,omitempty"`
	SwapTotal uint64 `json:"swapTotal,omitempty"`
}

// HostCPUInfo is one physical CPU package (PhysicalID-keyed in madmin).
type HostCPUInfo struct {
	ModelName  string  `json:"modelName,omitempty"`
	VendorID   string  `json:"vendorId,omitempty"`
	Mhz        float64 `json:"mhz,omitempty"`
	Cores      int     `json:"cores,omitempty"`
	PhysicalID string  `json:"physicalId,omitempty"`
}

// HostNICInfo is one NIC; madmin doesn't expose link speed here, only the
// driver/firmware names. Link speed still needs to come from SSH ethtool.
type HostNICInfo struct {
	Interface       string `json:"interface,omitempty"`
	Driver          string `json:"driver,omitempty"`
	FirmwareVersion string `json:"firmwareVersion,omitempty"`
}
