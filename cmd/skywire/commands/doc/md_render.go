//go:build !tinygo

// Package doc cmd/skywire/commands/doc/md_render.go
package doc

import (
	"bytes"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	ghtml "github.com/yuin/goldmark/renderer/html"
)

// mdToHTML renders markdown the way `skywire cli reward rules --html` already
// does, so the two agree on what markdown means here.
func mdToHTML(src []byte) []byte {
	var buf bytes.Buffer
	md := goldmark.New(
		goldmark.WithExtensions(extension.Strikethrough, extension.Table),
		// Heading anchors: without them nothing can link to a SECTION, only to
		// a file, and the desk needs to open this prose at the pairing procedure
		// rather than at the top of a long page.
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(ghtml.WithUnsafe()),
	)
	if err := md.Convert(src, &buf); err != nil {
		return []byte("<pre>" + err.Error() + "</pre>")
	}
	return buf.Bytes()
}
