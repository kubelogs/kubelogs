package web

import (
	"embed"
	"html/template"
	"io/fs"
)

//go:embed static templates
var assets embed.FS

// StaticFS returns the static files filesystem.
func StaticFS() (fs.FS, error) {
	return fs.Sub(assets, "static")
}

// Templates returns parsed HTML templates.
func Templates() (*template.Template, error) {
	return template.ParseFS(assets, "templates/*.html")
}
