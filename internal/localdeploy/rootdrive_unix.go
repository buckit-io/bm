//go:build !windows

package localdeploy

import (
	"os"
	"syscall"
)

func dataPathsOnRootDrive(goos string, dataPaths []string) []string {
	if goos != "darwin" {
		return nil
	}
	rootDev, ok := statDevice("/")
	if !ok {
		return nil
	}
	var out []string
	for _, p := range dataPaths {
		dev, ok := statDevice(p)
		if ok && dev == rootDev {
			out = append(out, p)
		}
	}
	return out
}

func statDevice(path string) (uint64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(stat.Dev), true
}
