//go:build ignore

// serve.go is a tiny static file server for the dmsg-wasm browser harness — no
// Python, no npm. It serves the built harness dir (default ./build/dmsg-wasm)
// with the correct application/wasm MIME so WebAssembly.instantiateStreaming
// works.
//
//	make tinygo-dmsg-wasm                       # build dmsg.wasm + stage the page
//	go run cmd/dmsg-wasm/testharness/serve.go   # serve it
//	open http://localhost:8085/
package main

import (
	"flag"
	"log"
	"mime"
	"net/http"
)

func main() {
	dir := flag.String("dir", "build/dmsg-wasm", "directory to serve")
	addr := flag.String("addr", ":8085", "listen address")
	flag.Parse()

	// Some environments lack a .wasm MIME mapping; set it explicitly so the
	// streaming compiler accepts the response.
	_ = mime.AddExtensionType(".wasm", "application/wasm")

	log.Printf("serving %s at http://localhost%s/", *dir, *addr)
	log.Fatal(http.ListenAndServe(*addr, http.FileServer(http.Dir(*dir)))) //nolint:gosec
}
