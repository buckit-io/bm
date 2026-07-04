//go:build windows

package localdeploy

import (
	"os"
	"path/filepath"
	"strings"
)

func dataPathsOnRootDrive(goos string, dataPaths []string) []string {
	if goos != "windows" {
		return nil
	}
	systemDrive := strings.TrimSpace(os.Getenv("SystemDrive"))
	if systemDrive == "" {
		systemDrive = filepath.VolumeName(os.Getenv("WINDIR"))
	}
	systemDrive = strings.ToLower(strings.TrimRight(systemDrive, `\/`))
	if systemDrive == "" {
		return nil
	}
	var out []string
	for _, p := range dataPaths {
		drive := strings.ToLower(strings.TrimRight(filepath.VolumeName(p), `\/`))
		if drive == systemDrive {
			out = append(out, p)
		}
	}
	return out
}
