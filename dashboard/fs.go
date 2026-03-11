package dashboard

import (
	"io/fs"
	"net/http"
)

// StaticFS returns the embedded static files as an http.FileSystem.
func StaticFS() (http.FileSystem, error) {
	sub, err := fs.Sub(Static, "static")
	if err != nil {
		return nil, err
	}
	return http.FS(sub), nil
}
