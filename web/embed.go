// Package web embeds the single-page user interface into the executable so
// the application ships as one self-contained file.
package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html app.js styles.css
var files embed.FS

// FS returns the embedded static files.
func FS() fs.FS { return files }
