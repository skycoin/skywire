//go:build tinygo

// Package clireward cmd/skywire-cli/commands/reward/rules_render_tinygo.go c4-vis-cli
//
// The TinyGo build carries no markdown renderer, so the rules are shown as
// their markdown source.
package clireward

import "html"

func rulesHTML(rules string) (string, error) {
	return "<pre>" + html.EscapeString(rules) + "</pre>", nil
}

func rulesTerminal(rules string, _, _ int) string { return rules }
