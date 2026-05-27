package bmassets

import (
	"embed"
	"io/fs"
)

// WebDist contains the built React bundle for release/runtime use.
//
//go:embed all:web/dist
var embeddedWebDist embed.FS

func WebDist() (fs.FS, error) {
	return fs.Sub(embeddedWebDist, "web/dist")
}
