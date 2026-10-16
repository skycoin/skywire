//go:build unix && !(cgo && !android && !ios && !tinygo && !withoutgio)

package commands

import "github.com/0magnet/websh/console"

func noWindow() string {
	return "this build has none, it needs cgo and no withoutgio tag"
}

func runWindow(console.Options) error { return nil }
