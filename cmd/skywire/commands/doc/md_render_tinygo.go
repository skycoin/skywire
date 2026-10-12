//go:build tinygo

// Package doc cmd/skywire/commands/doc/md_render_tinygo.go
//
// The TinyGo build carries no markdown renderer, so prose is served as its
// escaped markdown source.
package doc

import "html"

func mdToHTML(src []byte) []byte {
	return []byte("<pre>" + html.EscapeString(string(src)) + "</pre>")
}
