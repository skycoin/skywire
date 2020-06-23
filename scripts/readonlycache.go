/*
simple static file server in go
*/
package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	port := flag.String("p", "8079", "port to serve on")
	directory := flag.String("d", "/var/cache/apt/repo", "the directory of static file to host")
	flag.Parse()
	http.Handle("/", http.FileServer(http.Dir(*directory)))
	log.Fatal(http.ListenAndServe(":"+*port, nil))
}
