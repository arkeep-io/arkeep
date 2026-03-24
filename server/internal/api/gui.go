package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

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

	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, assets, "index.html")
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		reqPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")

		// Root request -> SPA entrypoint
		if reqPath == "." || reqPath == "" {
			serveIndex(w, r)
			return
		}

		// If the requested file exists in the embedded FS, serve it directly.
		if _, err := fs.Stat(assets, reqPath); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}

		// If it looks like a static file but does not exist, return 404.
		// Avoid serving index.html for missing assets like .js/.css/.ico/.mp4.
		if path.Ext(reqPath) != "" {
			http.NotFound(w, r)
			return
		}

		// Otherwise assume it's a SPA route and serve index.html.
		serveIndex(w, r)
	}

	r.Get("/*", handler)
	r.Head("/*", handler)
}