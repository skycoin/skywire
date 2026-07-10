package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

//go:embed dist
var uiFS embed.FS

func main() {
	port := "8787"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}

	// Remove the "dist" prefix from the file system
	distFS, err := fs.Sub(uiFS, "dist")
	if err != nil {
		log.Fatal(err)
	}

	// Create a custom file server that handles SPA routing
	fileServer := http.FileServer(http.FS(distFS))
	
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the file directly
		path := filepath.Clean(r.URL.Path)
		if path == "/" {
			path = "index.html"
		}

		// Check if file exists
		if _, err := fs.Stat(distFS, path); os.IsNotExist(err) {
			// File doesn't exist, serve index.html for SPA routing
			r.URL.Path = "/"
		}
		
		fileServer.ServeHTTP(w, r)
	})

	addr := ":" + port
	log.Printf("Exchange Client UI server starting on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
