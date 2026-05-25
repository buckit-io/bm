package admin

import (
	"testing"
	"time"

	"github.com/shirou/gopsutil/v3/host"

	madmin "github.com/buckit-io/madmin-go/v3"
)

func TestMapHealthInfo_PivotsByAddr(t *testing.T) {
	ts := time.Unix(1_700_000_000, 0).UTC()
	src := &madmin.HealthInfo{
		Version:   madmin.HealthInfoVersion,
		TimeStamp: ts,
		Sys: madmin.SysInfo{
			OSInfo: []madmin.OSInfo{
				{
					NodeCommon: madmin.NodeCommon{Addr: "node-a:9000"},
					Info:       host.InfoStat{KernelVersion: "6.6.0", Platform: "ubuntu", PlatformVersion: "22.04"},
				},
				{
					NodeCommon: madmin.NodeCommon{Addr: "node-b:9000"},
					Info:       host.InfoStat{KernelVersion: "5.15.0", Platform: "debian"},
				},
			},
			MemInfo: []madmin.MemInfo{
				{NodeCommon: madmin.NodeCommon{Addr: "node-a:9000"}, Total: 64 << 30, SwapSpaceTotal: 2 << 30},
				{NodeCommon: madmin.NodeCommon{Addr: "node-b:9000"}, Total: 32 << 30},
			},
			CPUInfo: []madmin.CPUs{
				{
					NodeCommon: madmin.NodeCommon{Addr: "node-a:9000"},
					CPUs: []madmin.CPU{
						{ModelName: "Xeon Gold", Mhz: 3200, Cores: 16, PhysicalID: "0"},
						{ModelName: "Xeon Gold", Mhz: 3200, Cores: 16, PhysicalID: "1"},
					},
				},
			},
			NetInfo: []madmin.NetInfo{
				{NodeCommon: madmin.NodeCommon{Addr: "node-a:9000"}, Interface: "eth0", Driver: "ixgbe"},
				{NodeCommon: madmin.NodeCommon{Addr: "node-a:9000"}, Interface: "", Driver: ""}, // skipped
			},
		},
	}

	got := mapHealthInfo(src)
	if got == nil {
		t.Fatal("nil result")
	}
	if got.Version != madmin.HealthInfoVersion || !got.Timestamp.Equal(ts) {
		t.Fatalf("metadata mismatch: %+v", got)
	}
	if len(got.Hosts) != 2 {
		t.Fatalf("want 2 hosts, got %d", len(got.Hosts))
	}
	// Sorted by addr — node-a first.
	a, b := got.Hosts[0], got.Hosts[1]
	if a.Addr != "node-a:9000" || b.Addr != "node-b:9000" {
		t.Fatalf("hosts not sorted: %v / %v", a.Addr, b.Addr)
	}
	if a.OS == nil || a.OS.KernelVersion != "6.6.0" || a.OS.Platform != "ubuntu" {
		t.Fatalf("node-a OS missing: %+v", a.OS)
	}
	if a.Mem == nil || a.Mem.Total != 64<<30 || a.Mem.SwapTotal != 2<<30 {
		t.Fatalf("node-a Mem missing: %+v", a.Mem)
	}
	if len(a.CPUs) != 2 || a.CPUs[0].ModelName != "Xeon Gold" || a.CPUs[1].PhysicalID != "1" {
		t.Fatalf("node-a CPUs missing: %+v", a.CPUs)
	}
	if len(a.NICs) != 1 || a.NICs[0].Interface != "eth0" || a.NICs[0].Driver != "ixgbe" {
		t.Fatalf("node-a NICs missing: %+v", a.NICs)
	}
	if b.OS == nil || b.OS.KernelVersion != "5.15.0" {
		t.Fatalf("node-b OS missing: %+v", b.OS)
	}
	if b.Mem == nil || b.Mem.Total != 32<<30 {
		t.Fatalf("node-b Mem missing: %+v", b.Mem)
	}
}

func TestMapHealthInfo_SurfacesErrors(t *testing.T) {
	src := &madmin.HealthInfo{
		Sys: madmin.SysInfo{
			OSInfo: []madmin.OSInfo{
				{NodeCommon: madmin.NodeCommon{Addr: "node-a:9000", Error: "unsupported operating system darwin"}},
			},
			MemInfo: []madmin.MemInfo{
				{NodeCommon: madmin.NodeCommon{Addr: "node-a:9000"}, Total: 16 << 30},
			},
		},
	}
	got := mapHealthInfo(src)
	if len(got.Hosts) != 1 {
		t.Fatalf("want 1 host, got %d", len(got.Hosts))
	}
	h := got.Hosts[0]
	if h.OS != nil {
		t.Fatalf("OS should be nil when osinfo errored: %+v", h.OS)
	}
	if len(h.Errors) != 1 || h.Errors[0] != "osinfo: unsupported operating system darwin" {
		t.Fatalf("missing error: %v", h.Errors)
	}
	if h.Mem == nil || h.Mem.Total != 16<<30 {
		t.Fatalf("Mem should still populate alongside errored OS: %+v", h.Mem)
	}
}

func TestMapHealthInfo_Nil(t *testing.T) {
	if got := mapHealthInfo(nil); got != nil {
		t.Fatalf("want nil for nil input, got %+v", got)
	}
}
