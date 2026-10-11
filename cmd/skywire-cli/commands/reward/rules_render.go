//go:build !tinygo

// Package clireward cmd/skywire-cli/commands/reward/rules_render.go c4-vis-cli
package clireward

import (
	"bytes"

	markdown "github.com/MichaelMure/go-term-markdown"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

func rulesHTML(rules string) (string, error) {
	var buf bytes.Buffer
	md := goldmark.New(
		goldmark.WithExtensions(extension.Strikethrough),
		goldmark.WithRendererOptions(html.WithXHTML()), // Optional: add XHTML compatibility
	)
	if err := md.Convert([]byte(rules), &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func rulesTerminal(rules string, width, leftPad int) string {
	return string(markdown.Render(rules, width, leftPad))
}
