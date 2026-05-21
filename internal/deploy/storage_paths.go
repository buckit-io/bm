package deploy

import "path"

const storageSubdirName = "buckit"

func storagePathForMount(mount string) string {
	return path.Join(mount, storageSubdirName)
}

func storagePathsForMounts(mounts []string) []string {
	paths := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		paths = append(paths, storagePathForMount(mount))
	}
	return paths
}
