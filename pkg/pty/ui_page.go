//go:build !mobile

// Package pty pkg/pty/ui_page.go c3-vis-pty
package pty

import (
	"net/http"

	"github.com/0magnet/websh/term"
)

// termPage is the terminal page: websh, which opens pty against the address
// it was served from. github.com/0magnet/websh/PTY.md is the protocol.
var termPage = term.Handler(func(r *http.Request) term.Page {
	return term.Page{Title: "skywire pty", Host: "skywire", Run: "pty " + shellQuote(pageTarget(r))}
})
