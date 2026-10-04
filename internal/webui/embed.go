package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var embeddedFiles embed.FS

// FileSystem returns the embedded Vite distribution for net/http static serving.
func FileSystem() http.FileSystem {
	distribution, err := fs.Sub(embeddedFiles, "dist")
	if err != nil {
		panic(err)
	}
	return http.FS(distribution)
}
