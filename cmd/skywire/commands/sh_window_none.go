//go:build unix && (ios || tinygo || (android && !cgo))

package commands

import "github.com/0magnet/websh/console"

func noWindow() string { return "this build has none" }

func runWindow(console.Options, string) error { return nil }
