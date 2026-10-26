//go:build mobile

// Package pty pkg/pty/ui_page_mobile.go c3-vis-pty
package pty

import "net/http"

// termPage is absent from the mobile build: its page is a compiled wasm module
// F-Droid would not accept. A hypervisor's /pty/<pk> reaches the same terminal.
var termPage = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "The web terminal page is not in the mobile build. Open it from a hypervisor at /pty/<pk>.", http.StatusNotImplemented)
})
