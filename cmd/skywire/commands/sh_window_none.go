//go:build unix && (android || ios || tinygo)

package commands

import "github.com/0magnet/websh/console"

func noWindow() string { return "this build has none" }

func runWindow(console.Options, string) error { return nil }
