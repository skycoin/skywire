package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
)

//go:embed static
var uiFS embed.FS

func main() {
	port := "8787"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}

	// Strip the "static" prefix so paths are served from the embed root.
	distFS, err := fs.Sub(uiFS, "static")
	if err != nil {
		log.Fatal(err)
	}

	// Create a custom file server that handles SPA routing
	fileServer := http.FileServer(http.FS(distFS))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Resolve the request to an embed-relative path (no leading slash,
		// which io/fs requires). Empty means the root document.
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}

		// If the requested file is not part of the built UI, fall back to
		// index.html so client-side (SPA) routes resolve correctly.
		if _, err := fs.Stat(distFS, name); err != nil {
			r.URL.Path = "/"
		}

		fileServer.ServeHTTP(w, r)
	})

	addr := ":" + port
	log.Printf("Exchange Client UI server starting on http://localhost%s", addr) //nolint
	log.Fatal(http.ListenAndServe(addr, nil))                                    //nolint
}
