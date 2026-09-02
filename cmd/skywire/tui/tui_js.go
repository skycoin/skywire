//go:build js && wasm

// Package tui cmd/skywire/tui/tui_js.go c5-cli-tui
//
// js/wasm stand-in for the interactive console. Install (install.go) already
// degrades gracefully: when Run errors it prints the note to stderr and falls
// back to the plain help that was asked for. A browser-hosted terminal that
// can drive bubbletea over explicit I/O can lift this later.
package tui

import (
	"errors"

	"github.com/spf13/cobra"
)

// Run reports that the interactive console is unavailable in the browser build.
func Run(_, _ *cobra.Command) error {
	return errors.New("interactive console is not available in the browser build")
}
