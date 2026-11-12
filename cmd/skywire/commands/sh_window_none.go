//go:build unix && (ios || tinygo || (android && !cgo))

// Package commands cmd/skywire/commands/sh_window_none.go c4-vis-cli
package commands

import "github.com/0magnet/websh/console"

func noWindow() string { return "this build has none" }

func runWindow(console.Options, string, []string) error { return nil }
