// Package pty pkg/pty/ui_target.go c3-vis-pty
package pty

import (
	"net/http"
	"path"
	"strings"
)

// pageTarget is the address the terminal page's pty connects to: this page,
// relative, with its query (commands=, scheme=) kept. It is always "./" and one
// path segment, so a crafted link cannot point the page at another server.
func pageTarget(r *http.Request) string {
	t := "./"
	if !strings.HasSuffix(r.URL.Path, "/") {
		t += path.Base("/" + r.URL.Path)
	}
	q := r.URL.Query()
	q.Del("asset")
	q.Del("sid")
	if e := q.Encode(); e != "" {
		t += "?" + e
	}
	return t
}

// shellQuote quotes s as one word for websh, which runs the page's command.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
