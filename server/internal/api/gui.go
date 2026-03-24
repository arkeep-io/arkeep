package api

import (
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// MountGUI registers the embedded GUI static files on the router as a
// catch-all. All requests that do not match an /api/v1 route are served
// from the embedded filesystem, enabling Vue Router history mode.
// Pass nil to disable GUI serving (useful in tests).
func MountGUI(r *chi.Mux, assets fs.FS) {
	if assets == nil {
		return
	}
	fileServer := http.FileServer(http.FS(assets))

	// Serve Vite-generated static assets directly
	r.Get("/assets/*", fileServer.ServeHTTP)
	r.Get("/favicon.ico", fileServer.ServeHTTP)
	r.Get("/manifest.webmanifest", fileServer.ServeHTTP)
	r.Get("/registerSW.js", fileServer.ServeHTTP)
	r.Get("/sw.js", fileServer.ServeHTTP)

	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, assets, "index.html")
	})
}
