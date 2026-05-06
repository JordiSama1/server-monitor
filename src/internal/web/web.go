// Package web embeds the static dashboard (HTML/CSS/JS) into the Go
// binary using embed.FS, so the deployed server-monitor is a single
// self-contained file with no external asset dependencies.
//
// FileServer returns an http.Handler suitable for mounting at "/" on
// any router. Asset paths are addressed relative to assets/ in the
// source tree (so /index.html maps to assets/index.html).
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed assets
var assets embed.FS

// FileServer returns an http.Handler serving the embedded assets tree.
// Panics only if the embed directive failed at build time, which would
// be a programmer error caught immediately on first run.
func FileServer() http.Handler {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		panic("embed: " + err.Error())
	}
	return http.FileServer(http.FS(sub))
}
