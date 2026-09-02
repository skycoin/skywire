//go:build js && wasm

// Package cliskychat cmd/skywire-cli/commands/skychat/chat_tui_js.go c5-cli-skychat
package cliskychat

import "errors"

var errNoTUIJS = errors.New("skychat: interactive TUI is not available in the browser build")

// runChatTUI: stub — no interactive terminal in the browser build.
func runChatTUI(_, _, _ string) error { return errNoTUIJS }

// runUnifiedTUI: stub — no interactive terminal in the browser build.
func runUnifiedTUI(_, _ string) error { return errNoTUIJS }
