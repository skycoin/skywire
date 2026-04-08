// Package main provides a simple HTTP server for e2e testing

package main

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		//nolint:errcheck,gosec
		w.Write([]byte("DMSG E2E Test Server\n"))
		//nolint:errcheck,gosec
		w.Write([]byte("Path: " + r.URL.Path + "\n"))
		//nolint:errcheck,gosec
		w.Write([]byte("Method: " + r.Method + "\n"))
		//nolint:errcheck,gosec
		w.Write([]byte("Host: " + r.Host + "\n"))
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		//nolint:errcheck,gosec
		w.Write([]byte("OK"))
	})

	http.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		for k, v := range query {
			msg := fmt.Sprintf("%s: %v\n", k, v)
			//nolint:errcheck,gosec
			w.Write([]byte(msg))
		}
	})

	addr := ":8086"
	log.Printf("Starting HTTP test server on %s", addr)

	server := &http.Server{
		Addr:              addr,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Fatal(server.ListenAndServe())
}
