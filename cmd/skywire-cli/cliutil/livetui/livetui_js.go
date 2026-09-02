//go:build js && wasm

// Package livetui cmd/skywire-cli/cliutil/livetui/livetui_js.go c5-cli-util
//
// js/wasm stand-in: -L/--live views need an interactive terminal loop the
// browser build does not provide. The exported surface matches livetui.go so
// callers compile unchanged; Run reports the limitation.
package livetui

import (
	"context"
	"errors"
	"time"
)

// Refresh produces one frame of content for the live view.
type Refresh func(ctx context.Context) (string, error)

// Options tunes a Run invocation.
type Options struct {
	// Title shown in the header. Defaults to "skywire live".
	Title string
	// Interval between Refresh calls. Defaults to 1s.
	Interval time.Duration
}

// Run reports that live TUI views are unavailable in the browser build.
func Run(_ Refresh, _ Options) error {
	return errors.New("live view: interactive TUI is not available in the browser build")
}
