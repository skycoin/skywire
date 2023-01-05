// Package main example/http-server/server.go
package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/skycoin/skywire/example/http-server/html"
)

func homepage(w http.ResponseWriter, r *http.Request) {
	p := html.HomepageParams{
		Title:   "Homepage",
		Message: "Hello from Homepage",
	}
	err := html.Homepage(w, p)
	if err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func main() {

	http.HandleFunc("/", homepage)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(html.FS()))))
	srv := &http.Server{ //nolint gosec
		Addr:         ":8080",
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	fmt.Println("serving on http://localhost:8080")
	err := srv.ListenAndServe()
	if err != nil {
		fmt.Printf("error serving: %v\n", err)
	}

}
