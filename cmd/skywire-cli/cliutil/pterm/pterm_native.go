//go:build !(js && wasm)

// Package pterm cmd/skywire-cli/cliutil/pterm/pterm_native.go c5-cli-util
//
// Shadow of github.com/pterm/pterm exposing exactly the surface the skywire
// CLI uses. Command code imports THIS package with the same package name, so
// call sites are byte-identical; on native builds everything aliases the real
// pterm, and on js/wasm (pterm's keyboard dependency has no js support) a
// plain-ANSI implementation stands in (pterm_js.go). Add aliases here — and a
// js twin there — when a command needs more of pterm.
package pterm

import "github.com/pterm/pterm"

// Sprint-style color functions.
var (
	Red     = pterm.Red
	Green   = pterm.Green
	Blue    = pterm.Blue
	Cyan    = pterm.Cyan
	Magenta = pterm.Magenta
	Yellow  = pterm.Yellow
	White   = pterm.White
	Black   = pterm.Black
)

// Background styles (value.Sprint(...) usage).
var (
	BgRed     = pterm.BgRed
	BgBlue    = pterm.BgBlue
	BgMagenta = pterm.BgMagenta
)

// Println mirrors pterm.Println.
var Println = pterm.Println

// Tree rendering.
type (
	// TreeNode is pterm.TreeNode.
	TreeNode = pterm.TreeNode
	// LeveledList is pterm.LeveledList.
	LeveledList = pterm.LeveledList
	// LeveledListItem is pterm.LeveledListItem.
	LeveledListItem = pterm.LeveledListItem
)

// DefaultTree is pterm.DefaultTree.
var DefaultTree = pterm.DefaultTree
