package web

import (
	"embed"
	"io/fs"
)

// Build React before building the production executable. Keeping this directory
// present also lets backend tests run in a fresh checkout without Node tooling.
//
//go:embed all:dist
var files embed.FS

func Assets() fs.FS {
	result, err := fs.Sub(files, "dist")
	if err != nil {
		panic(err)
	}
	return result
}
