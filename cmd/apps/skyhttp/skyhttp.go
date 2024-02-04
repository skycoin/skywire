package main

import (
	"log"
	"net/http"

	"github.com/skycoin/skywire/internal/skyhttp"
)

func main() {
	// Create a new HTTP server with the handleRequest function as the handler
	server := http.Server{
		Addr:    ":8090",
		Handler: http.HandlerFunc(skyhttp.Handler),
	}

	// Start the server and log any errors
	log.Println("Starting proxy server on :8080")
	err := server.ListenAndServe()
	if err != nil {
		log.Fatal("Error starting proxy server: ", err)
	}
}
