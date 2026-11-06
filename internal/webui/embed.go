// Package webui serve o frontend estático embutido no binário.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Dist devolve o sub-FS dist do frontend embutido.
func Dist() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err) // dist é garantido pelo go:embed
	}
	return sub
}
